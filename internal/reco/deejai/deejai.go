// Package deejai is a Go port of teticio/deej-ai.online-app's playlist walk
// (backend/deejai.py): make_playlist for a single seed, join_the_dots between
// two or more, additive Gaussian "noise" on the running query, and the
// id / display-string / back-to-back-artist dedup. It implements
// ports.RecommendationEngine over a ports.Catalog + ports.SimilarityEngine.
package deejai

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// searchK is how many ranked candidates to pull from the similarity engine per
// step. It only needs to exceed the number of tracks the dedup rules can reject
// before a valid one appears — a few thousand is comfortably enough at any
// catalog size, and covers the whole (small) test catalog.
const searchK = 4096

// Engine implements ports.RecommendationEngine.
type Engine struct {
	cat ports.Catalog
	sim ports.SimilarityEngine
}

// New builds an engine over a catalog and its similarity index.
func New(cat ports.Catalog, sim ports.SimilarityEngine) *Engine {
	return &Engine{cat: cat, sim: sim}
}

type step struct {
	ref    core.TrackRef
	kind   string
	detail string
}

// Build implements ports.RecommendationEngine. It is deterministic given
// (intent, catalog, intent.Seed); when intent.Seed is 0 a time-based seed is
// chosen and echoed back on the Playlist.
func (e *Engine) Build(_ context.Context, intent core.MusicIntent) (core.Playlist, error) {
	intent = intent.Normalized()

	seeds := e.resolveSeeds(intent)
	if len(seeds) == 0 {
		return core.Playlist{}, core.ErrNoSeeds
	}

	seed := intent.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // not cryptographic; reproducibility is the point

	weights := [2]float32{float32(intent.Creativity), float32(1 - intent.Creativity)}

	var steps []step
	if intent.Mode == core.ModeJourney && len(seeds) >= 2 {
		steps = e.journey(seeds, weights, intent, rng)
	} else {
		steps = e.similar(seeds, weights, intent, rng)
	}

	pl := core.Playlist{Mode: intent.Mode, Seed: seed, Intent: intent}
	for _, s := range steps {
		pl.Tracks = append(pl.Tracks, s.ref)
		pl.Rationale = append(pl.Rationale, core.StepReason{TrackID: s.ref.ID, Kind: s.kind, Detail: s.detail})
	}
	return pl, nil
}

// --- seed resolution -------------------------------------------------------

func (e *Engine) resolveSeeds(intent core.MusicIntent) []core.TrackRef {
	seen := map[string]struct{}{}
	var out []core.TrackRef
	add := func(ref core.TrackRef) {
		if ref.ID == "" {
			return
		}
		if _, dup := seen[ref.ID]; dup {
			return
		}
		seen[ref.ID] = struct{}{}
		out = append(out, ref)
	}
	for _, id := range intent.Seeds.TrackIDs {
		if m, ok := e.cat.Meta(id); ok {
			add(m.Ref)
		}
	}
	for _, q := range intent.Seeds.Queries {
		if hits := e.cat.Resolve(q, 1); len(hits) > 0 {
			add(hits[0])
		}
	}
	return out
}

// --- make_playlist -------------------------------------------------------

func (e *Engine) similar(seeds []core.TrackRef, weights [2]float32, intent core.MusicIntent, rng *rand.Rand) []step {
	d := e.cat.Dim()
	f := newFilter(intent, seeds)

	steps := make([]step, 0, intent.Count)
	for _, s := range seeds {
		steps = append(steps, step{ref: s, kind: "seed"})
		f.markUsed(s)
	}

	for len(steps) < intent.Count {
		window := steps[maxInt(0, len(steps)-intent.Lookback):]

		audioSum := make([]float32, d)
		trackSum := make([]float32, d)
		for _, w := range window {
			v, ok := e.cat.Vectors(w.ref.ID)
			if !ok {
				continue
			}
			addNormalized(audioSum, v.Audio)
			addNormalized(trackSum, v.Track)
		}
		applyNoise(audioSum, trackSum, intent.Noise, rng)

		matches := e.sim.Search(ports.SimilarityQuery{
			AudioSum: audioSum,
			TrackSum: trackSum,
			Weights:  weights,
			K:        searchK,
			Exclude:  f.excludeIDs(),
		})
		if len(matches) == 0 {
			break
		}

		prevArtist := artistPrefix(steps[len(steps)-1].ref.Display())
		chosen, rank, ok := f.pick(e.cat, matches, prevArtist)
		kind, detail := "nearest", fmt.Sprintf("rank %d of %d", rank+1, len(matches))
		if !ok {
			chosen = e.refOf(matches[len(matches)-1].ID)
			kind, detail = "fallback", "no candidate passed the dedup rules"
		} else if rank > 0 {
			detail = fmt.Sprintf("rank %d — %d closer skipped by dedup", rank+1, rank)
		}
		if intent.Noise > 0 && kind == "nearest" {
			detail += fmt.Sprintf(", noise %.2f", intent.Noise)
		}

		steps = append(steps, step{ref: chosen, kind: kind, detail: detail})
		f.markUsed(chosen)
	}
	return steps
}

// --- join_the_dots -----------------------------------------------------

func (e *Engine) journey(seeds []core.TrackRef, weights [2]float32, intent core.MusicIntent, rng *rand.Rand) []step {
	d := e.cat.Dim()
	size := intent.Count // intermediates per segment (upstream `size`)
	f := newFilter(intent, seeds)
	for _, s := range seeds { // journey dedups display strings against the waypoints only
		f.reserveDisplay(s)
	}

	var steps []step
	for si := 0; si < len(seeds)-1; si++ {
		start, end := seeds[si], seeds[si+1]
		startV, _ := e.cat.Vectors(start.ID)
		endV, _ := e.cat.Vectors(end.ID)
		sa, st := l2normalized(startV.Audio), l2normalized(startV.Track)
		ea, et := l2normalized(endV.Audio), l2normalized(endV.Track)

		steps = append(steps, step{ref: start, kind: "seed", detail: "waypoint"})
		f.markUsedID(start)

		for i := 0; i < size; i++ {
			g := float32(i+1) / float32(size+1)
			fh := float32(size-i) / float32(size+1)

			audioSum := make([]float32, d)
			trackSum := make([]float32, d)
			for k := 0; k < d; k++ {
				audioSum[k] = fh*sa[k] + g*ea[k]
				trackSum[k] = fh*st[k] + g*et[k]
			}
			applyNoise(audioSum, trackSum, intent.Noise, rng)

			matches := e.sim.Search(ports.SimilarityQuery{
				AudioSum: audioSum,
				TrackSum: trackSum,
				Weights:  weights,
				K:        searchK,
				Exclude:  f.excludeIDs(),
			})
			if len(matches) == 0 {
				break
			}
			prevArtist := artistPrefix(steps[len(steps)-1].ref.Display())
			chosen, rank, ok := f.pick(e.cat, matches, prevArtist)
			kind, detail := "interp", fmt.Sprintf("segment %d, t=%.2f", si+1, g)
			if !ok {
				chosen = e.refOf(matches[len(matches)-1].ID)
				kind = "fallback"
			}
			_ = rank
			steps = append(steps, step{ref: chosen, kind: kind, detail: detail})
			f.markUsedID(chosen)
		}
	}
	steps = append(steps, step{ref: seeds[len(seeds)-1], kind: "seed", detail: "waypoint"})
	return steps
}

func (e *Engine) refOf(id string) core.TrackRef {
	if m, ok := e.cat.Meta(id); ok {
		return m.Ref
	}
	return core.TrackRef{ID: id}
}

// --- vector helpers ----------------------------------------------------

func addNormalized(dst, src []float32) {
	n := l2norm(src)
	if n == 0 {
		return
	}
	for i := range dst {
		dst[i] += src[i] / n
	}
}

func l2normalized(v []float32) []float32 {
	out := make([]float32, len(v))
	n := l2norm(v)
	if n == 0 {
		return out
	}
	for i := range v {
		out[i] = v[i] / n
	}
	return out
}

func l2norm(v []float32) float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return float32(math.Sqrt(s))
}

// applyNoise adds zero-mean Gaussian noise with std = noise*||v|| to each
// sub-vector, matching most_similar's np.random.normal(0, noise*norm, 100).
// A no-op when noise == 0, so seed choice never affects a noise-free run.
func applyNoise(audioSum, trackSum []float32, noise float64, rng *rand.Rand) {
	if noise <= 0 {
		return
	}
	for _, v := range [][]float32{audioSum, trackSum} {
		std := noise * float64(l2norm(v))
		for i := range v {
			v[i] += float32(rng.NormFloat64() * std)
		}
	}
}

func artistPrefix(display string) string {
	if i := strings.Index(display, " - "); i >= 0 {
		return display[:i]
	}
	if len(display) == 0 {
		return ""
	}
	return display[:len(display)-1] // mirror Python str.find(' - ') == -1 → s[:-1]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ ports.RecommendationEngine = (*Engine)(nil)
