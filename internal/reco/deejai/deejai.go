// Package deejai implements deterministic walks over the Deej-AI embedding catalog.
package deejai

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	mathrand "math/rand"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

const searchK = 4096

const AlgorithmVersion = "deejai/v4"

type Engine struct {
	cat      ports.Catalog
	sim      ports.SimilarityEngine
	resolver ports.ReferenceResolver
}

func New(cat ports.Catalog, sim ports.SimilarityEngine, resolvers ...ports.ReferenceResolver) *Engine {
	var resolver ports.ReferenceResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	} else if candidate, ok := cat.(ports.ReferenceResolver); ok {
		resolver = candidate
	}
	return &Engine{cat: cat, sim: sim, resolver: resolver}
}

func (*Engine) AlgorithmVersion() string { return AlgorithmVersion }

type step struct {
	ref    core.TrackRef
	kind   string
	detail string
}

func (e *Engine) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	intent = intent.Normalized()
	if e.resolver != nil {
		var issues []resolution.Issue
		intent, issues = resolution.Apply(e.resolver, intent)
		if err := resolution.BlockingError(issues); err != nil {
			return core.Playlist{}, err
		}
	}
	references := e.resolve(intent.Seeds)
	if intent.Mode == core.ModeJourney {
		if anchors := e.journeyReferences(intent); len(anchors) >= 2 {
			references = anchors
		}
	}
	required := e.resolve(intent.Required)
	if len(references) == 0 && len(required) == 0 {
		return core.Playlist{}, core.ErrNoSeeds
	}
	if requestedSeedCount(intent.Required) > 0 && len(required) == 0 {
		return core.Playlist{}, fmt.Errorf("%w: none of the required tracks resolved", core.ErrRequiredTrackConflict)
	}
	if intent.Count < len(required) {
		return core.Playlist{}, fmt.Errorf("%w: requested %d tracks but %d are required",
			core.ErrCountBelowRequired, intent.Count, len(required))
	}

	f := newFilter(intent, references, required)
	for _, ref := range required {
		if f.hardExcluded(ref) {
			return core.Playlist{}, fmt.Errorf("%w: %q is required and excluded",
				core.ErrRequiredTrackConflict, ref.Display())
		}
	}

	seed := intent.Seed
	if seed.IsZero() {
		seed = randomSeed()
	}
	seedValue, err := seed.Int64()
	if err != nil {
		return core.Playlist{}, fmt.Errorf("seed: %w", err)
	}
	intent.Seed = seed
	rng := mathrand.New(mathrand.NewSource(seedValue)) //nolint:gosec // deterministic recommendation noise
	weights := [2]float32{float32(intent.Creativity), float32(1 - intent.Creativity)}

	var steps []step
	if intent.Mode == core.ModeJourney {
		steps, err = e.journey(ctx, references, required, weights, intent, rng, f)
	} else {
		steps, err = e.similar(ctx, references, required, weights, intent, rng, f)
	}
	if err != nil {
		return core.Playlist{}, err
	}

	pl := core.Playlist{Mode: intent.Mode, Seed: seed, Intent: intent}
	for _, s := range steps {
		pl.Tracks = append(pl.Tracks, s.ref)
		pl.Rationale = append(pl.Rationale, core.StepReason{
			TrackID: s.ref.ID,
			Kind:    s.kind,
			Detail:  s.detail,
		})
	}
	if len(pl.Tracks) < intent.Count {
		pl.Notices = append(pl.Notices, core.PlaylistNotice{
			Code:      "eligible_tracks_exhausted",
			Detail:    "eligible catalog tracks were exhausted without relaxing exclusions or duplicate protection",
			Requested: intent.Count,
			Actual:    len(pl.Tracks),
		})
	}
	return pl, nil
}

func randomSeed() core.RNGSeed {
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		// crypto/rand failure is extraordinarily rare. A fixed non-zero value is
		// still preferable to silently losing seed width or producing invalid JSON.
		return core.NewRNGSeed(1)
	}
	value := binary.LittleEndian.Uint64(raw[:])
	if value == 0 {
		value = 1
	}
	return core.NewRNGSeed(value)
}

func requestedSeedCount(s core.IntentSeeds) int {
	return len(s.TrackIDs) + len(s.Queries)
}

func (e *Engine) resolve(seeds core.IntentSeeds) []core.TrackRef {
	seenIDs := map[string]struct{}{}
	seenRecordings := map[string]struct{}{}
	var out []core.TrackRef
	add := func(ref core.TrackRef) {
		if ref.ID == "" {
			return
		}
		if _, duplicate := seenIDs[ref.ID]; duplicate {
			return
		}
		key := core.ProvisionalRecordingKey(ref)
		if _, duplicate := seenRecordings[key]; duplicate {
			return
		}
		seenIDs[ref.ID] = struct{}{}
		seenRecordings[key] = struct{}{}
		out = append(out, ref)
	}
	for _, id := range seeds.TrackIDs {
		if meta, ok := e.cat.Meta(id); ok {
			add(meta.Ref)
		}
	}
	if e.resolver == nil {
		for _, query := range seeds.Queries {
			if hits := e.cat.Resolve(query, 1); len(hits) > 0 {
				add(hits[0])
			}
		}
	}
	return out
}

func (e *Engine) journeyReferences(intent core.MusicIntent) []core.TrackRef {
	references := intent.Journey.Waypoints
	if len(references) < 2 {
		references = intent.References
	}
	var seeds core.IntentSeeds
	for _, reference := range references {
		if reference.Influence != core.InfluencePositive {
			continue
		}
		if reference.Resolution != nil && reference.Resolution.Selected != nil && len(reference.Resolution.Selected.Representatives) > 0 {
			seeds.TrackIDs = append(seeds.TrackIDs, reference.Resolution.Selected.Representatives[0].TrackID)
		} else if reference.TrackID != "" {
			seeds.TrackIDs = append(seeds.TrackIDs, reference.TrackID)
		} else {
			seeds.Queries = append(seeds.Queries, reference.Query)
		}
	}
	return e.resolve(seeds)
}

func (e *Engine) similar(
	ctx context.Context,
	references, required []core.TrackRef,
	weights [2]float32,
	intent core.MusicIntent,
	rng *mathrand.Rand,
	f *filter,
) ([]step, error) {
	steps := make([]step, 0, intent.Count)
	referenceWeights := resolvedReferenceWeights(intent)
	history := append([]core.TrackRef(nil), references...)
	if len(history) == 0 {
		history = append(history, required...)
	}
	for _, ref := range required {
		steps = append(steps, step{ref: ref, kind: "required", detail: "required track"})
		f.markUsed(ref)
		if !containsID(history, ref.ID) {
			history = append(history, ref)
		}
	}

	for len(steps) < intent.Count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		window := history[maxInt(0, len(history)-intent.Lookback):]
		audioSum, trackSum := e.vectorSums(window, referenceWeights)
		applyNoise(audioSum, trackSum, intent.Noise, rng)

		prevArtist := ""
		if len(steps) > 0 {
			prevArtist = core.NormalizeIdentityPart(steps[len(steps)-1].ref.Artist)
		} else if len(history) > 0 {
			prevArtist = core.NormalizeIdentityPart(history[len(history)-1].Artist)
		}
		chosen, rank, searched, ok, err := e.pickExpanded(ctx, audioSum, trackSum, weights, f, prevArtist)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		detail := fmt.Sprintf("rank %d of %d", rank+1, searched)
		if rank > 0 {
			detail = fmt.Sprintf("rank %d — %d closer tracks excluded or duplicated", rank+1, rank)
		}
		if intent.Noise > 0 {
			detail += fmt.Sprintf(", noise %.2f", intent.Noise)
		}
		steps = append(steps, step{ref: chosen, kind: "nearest", detail: detail})
		f.markUsed(chosen)
		history = append(history, chosen)
	}
	return steps, nil
}

func (e *Engine) journey(
	ctx context.Context,
	references, required []core.TrackRef,
	weights [2]float32,
	intent core.MusicIntent,
	rng *mathrand.Rand,
	f *filter,
) ([]step, error) {
	anchors := references
	emitWaypoints := false
	if len(required) >= 2 {
		anchors = required
		emitWaypoints = true
	}
	if len(anchors) < 2 {
		return e.similar(ctx, references, required, weights, intent, rng, f)
	}

	steps := make([]step, 0, intent.Count)
	if !emitWaypoints {
		for _, ref := range required {
			steps = append(steps, step{ref: ref, kind: "required", detail: "required track"})
			f.markUsed(ref)
		}
	}
	intermediateSlots := intent.Count - len(steps)
	if emitWaypoints {
		intermediateSlots = intent.Count - len(required)
	}
	perSegment := distribute(intermediateSlots, len(anchors)-1)

	for segment := 0; segment < len(anchors)-1; segment++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start, end := anchors[segment], anchors[segment+1]
		if emitWaypoints {
			steps = append(steps, step{ref: start, kind: "required", detail: "required journey waypoint"})
			f.markUsed(start)
		}
		startVectors, startOK := e.cat.Vectors(start.ID)
		endVectors, endOK := e.cat.Vectors(end.ID)
		if !startOK || !endOK {
			continue
		}
		sa, st := l2normalized(startVectors.Audio), l2normalized(startVectors.Track)
		ea, et := l2normalized(endVectors.Audio), l2normalized(endVectors.Track)
		slots := perSegment[segment]

		for i := 0; i < slots; i++ {
			t := float32(i+1) / float32(slots+1)
			audioSum := interpolate(sa, ea, t)
			trackSum := interpolate(st, et, t)
			applyNoise(audioSum, trackSum, intent.Noise, rng)

			prevArtist := core.NormalizeIdentityPart(start.Artist)
			if len(steps) > 0 {
				prevArtist = core.NormalizeIdentityPart(steps[len(steps)-1].ref.Artist)
			}
			chosen, rank, _, ok, err := e.pickExpanded(ctx, audioSum, trackSum, weights, f, prevArtist)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			steps = append(steps, step{
				ref:    chosen,
				kind:   "interp",
				detail: fmt.Sprintf("segment %d, t=%.2f, rank %d", segment+1, t, rank+1),
			})
			f.markUsed(chosen)
		}
	}
	if emitWaypoints {
		last := required[len(required)-1]
		steps = append(steps, step{ref: last, kind: "required", detail: "required journey waypoint"})
		f.markUsed(last)
	}
	return steps, nil
}

func (e *Engine) pickExpanded(
	ctx context.Context,
	audioSum, trackSum []float32,
	weights [2]float32,
	f *filter,
	prevArtist string,
) (core.TrackRef, int, int, bool, error) {
	limit := e.sim.Len()
	if limit <= 0 {
		return core.TrackRef{}, 0, 0, false, nil
	}
	k := minInt(searchK, limit)
	for {
		matches, err := e.sim.Search(ctx, ports.SimilarityQuery{
			AudioSum: audioSum,
			TrackSum: trackSum,
			Weights:  weights,
			K:        k,
			Exclude:  f.excludeIDs(),
		})
		if err != nil {
			return core.TrackRef{}, 0, 0, false, err
		}
		chosen, rank, ok, err := f.pick(ctx, e.cat, matches, prevArtist)
		if err != nil {
			return core.TrackRef{}, 0, 0, false, err
		}
		if ok {
			return chosen, rank, len(matches), true, nil
		}
		if k == limit || len(matches) < k {
			return core.TrackRef{}, 0, len(matches), false, nil
		}
		k = minInt(k*2, limit)
	}
}

func (e *Engine) vectorSums(refs []core.TrackRef, weights map[string]float32) ([]float32, []float32) {
	audioSum := make([]float32, e.cat.Dim())
	trackSum := make([]float32, e.cat.Dim())
	for _, ref := range refs {
		if vectors, ok := e.cat.Vectors(ref.ID); ok {
			weight := weights[ref.ID]
			if weight == 0 {
				weight = 1
			}
			addNormalizedWeighted(audioSum, vectors.Audio, weight)
			addNormalizedWeighted(trackSum, vectors.Track, weight)
		}
	}
	return audioSum, trackSum
}

func resolvedReferenceWeights(intent core.MusicIntent) map[string]float32 {
	out := make(map[string]float32)
	for _, reference := range append(append([]core.IntentReference(nil), intent.References...), intent.Journey.Waypoints...) {
		if reference.Influence != core.InfluencePositive || reference.Resolution == nil || reference.Resolution.Selected == nil {
			continue
		}
		for _, representative := range reference.Resolution.Selected.Representatives {
			out[representative.TrackID] += float32(representative.Weight)
		}
	}
	return out
}

func distribute(total, buckets int) []int {
	out := make([]int, buckets)
	if buckets <= 0 || total <= 0 {
		return out
	}
	for i := range out {
		out[i] = total / buckets
		if i < total%buckets {
			out[i]++
		}
	}
	return out
}

func interpolate(a, b []float32, t float32) []float32 {
	out := make([]float32, len(a))
	for i := range out {
		out[i] = (1-t)*a[i] + t*b[i]
	}
	return out
}

func containsID(refs []core.TrackRef, id string) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

func addNormalizedWeighted(dst, src []float32, weight float32) {
	n := l2norm(src)
	if n == 0 {
		return
	}
	for i := range dst {
		dst[i] += weight * src[i] / n
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
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return float32(math.Sqrt(sum))
}

func applyNoise(audioSum, trackSum []float32, noise float64, rng *mathrand.Rand) {
	if noise <= 0 {
		return
	}
	for _, vector := range [][]float32{audioSum, trackSum} {
		std := noise * float64(l2norm(vector))
		for i := range vector {
			vector[i] += float32(rng.NormFloat64() * std)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ ports.RecommendationEngine = (*Engine)(nil)
var _ ports.VersionedRecommendationEngine = (*Engine)(nil)
