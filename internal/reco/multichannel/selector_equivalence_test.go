package multichannel

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

// referenceSelect is the direct, non-incremental MMR loop: it rescans the whole
// context for every candidate on every round. Select folds each newly chosen
// track into running terms instead, which is what makes it linear rather than
// quadratic in playlist length. This oracle pins the two to identical output —
// the standalone maxRedundancy/artistConcentration/albumConcentration/
// diversityPenalty helpers below are the component semantics it checks against.
func referenceSelect(s *MMRSelector, ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest) ports.SelectionResult {
	best := math.Inf(-1)
	for _, candidate := range candidates {
		best = math.Max(best, candidate.Scores.Total)
	}
	floor := math.Max(s.cfg.SelectionMinimumRelevance, best-s.cfg.SelectionRelevanceWindow)
	pool := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Scores.Total >= floor {
			pool = append(pool, candidate)
		}
	}
	result := ports.SelectionResult{Candidates: make([]core.Candidate, 0, request.Count)}
	contextTracks := append([]core.TrackRef(nil), request.Required...)
	contextTracks = append(contextTracks, request.Waypoints...)
	contextTracks = append(contextTracks, tailTracks(request.RecentSelections, maxContinuationAnchors)...)
	lambda := 1 - (1-s.cfg.MMRMinimumLambda)*clamp(request.Intent.Controls.ArtistDiversity, 0, 1)
	for len(result.Candidates) < request.Count && len(pool) > 0 {
		chosen := -1
		for index := range pool {
			candidate := &pool[index]
			candidate.Scores.SelectionRelevance = normalizedRelevance(candidate.Scores.Total, floor, best)
			candidate.Available.SelectionRelevance = true
			candidate.Scores.EmbeddingRedundancy, candidate.Available.EmbeddingRedundancy =
				s.maxRedundancy(candidate.Track, contextTracks, request.Intent)
			candidate.Scores.ArtistConcentration, candidate.Available.ArtistConcentration =
				artistConcentration(candidate.Track, contextTracks)
			candidate.Scores.AlbumConcentration, candidate.Available.AlbumConcentration =
				s.albumConcentration(candidate.Track, contextTracks)
			penalty := s.diversityPenalty(*candidate)
			candidate.Scores.MMR = lambda*candidate.Scores.SelectionRelevance - (1-lambda)*penalty
			candidate.Available.MMR = true
			if chosen < 0 || betterMMR(*candidate, pool[chosen]) {
				chosen = index
			}
		}
		selected := pool[chosen]
		result.Candidates = append(result.Candidates, selected)
		contextTracks = append(contextTracks, selected.Track)
		pool = append(pool[:chosen], pool[chosen+1:]...)
	}
	return result
}

func TestIncrementalSelectionMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260906))
	for trial := range 60 {
		const dim = 8
		n := 20 + rng.Intn(60)
		rows := make([]fakes.CatalogTrack, 0, n)
		for i := range n {
			a := make([]float32, dim)
			b := make([]float32, dim)
			for d := range dim {
				a[d] = rng.Float32()*2 - 1
				b[d] = rng.Float32()*2 - 1
			}
			row := fakes.CatalogTrack{
				ID: fmt.Sprintf("t%03d", i), Display: fmt.Sprintf("Artist %d - T%d", i%7, i),
				Audio: a, Track: b,
			}
			switch i % 4 { // mix reliable, unreliable and absent album metadata
			case 0:
				row.Album, row.AlbumReliable = fmt.Sprintf("Album %d", i%3), true
			case 1:
				row.Album, row.AlbumReliable = "Unverified", false
			}
			rows = append(rows, row)
		}
		cat := fakes.NewCatalog(dim, rows...)

		candidates := make([]core.Candidate, 0, n)
		for i := range n {
			candidates = append(candidates, core.Candidate{
				Track:  core.TrackRef{ID: rows[i].ID, Artist: fmt.Sprintf("Artist %d", i%7)},
				Scores: core.CandidateScores{Total: rng.Float64()*1.4 - .2},
			})
		}
		intent := core.MusicIntent{
			Count:    10,
			Controls: core.IntentControls{ArtistDiversity: rng.Float64(), AudioWeight: rng.Float64(), CooccurrenceWeight: rng.Float64()},
		}.Normalized()
		req := ports.SelectionRequest{Intent: intent, Count: 1 + rng.Intn(12)}
		if rng.Intn(2) == 0 {
			req.Required = []core.TrackRef{{ID: rows[0].ID, Artist: "Artist 0"}}
		}
		if rng.Intn(2) == 0 {
			req.RecentSelections = []core.TrackRef{{ID: rows[1].ID, Artist: "Artist 1"}, {ID: rows[2].ID, Artist: "Artist 2"}}
		}
		if rng.Intn(2) == 0 {
			req.Waypoints = []core.TrackRef{{ID: rows[3].ID, Artist: "Artist 3"}}
		}

		sel := NewSelector(cat, DefaultConfig())
		want := referenceSelect(sel, context.Background(), append([]core.Candidate(nil), candidates...), req)
		got, err := sel.Select(context.Background(), append([]core.Candidate(nil), candidates...), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Candidates) != len(want.Candidates) {
			t.Fatalf("trial %d: length %d != reference %d", trial, len(got.Candidates), len(want.Candidates))
		}
		for i := range want.Candidates {
			w, g := want.Candidates[i], got.Candidates[i]
			if w.Track.ID != g.Track.ID {
				t.Fatalf("trial %d pos %d: picked %s, reference picked %s", trial, i, g.Track.ID, w.Track.ID)
			}
			for _, f := range []struct {
				name   string
				w, g   float64
				wa, ga bool
			}{
				{"selectionRelevance", w.Scores.SelectionRelevance, g.Scores.SelectionRelevance, w.Available.SelectionRelevance, g.Available.SelectionRelevance},
				{"embeddingRedundancy", w.Scores.EmbeddingRedundancy, g.Scores.EmbeddingRedundancy, w.Available.EmbeddingRedundancy, g.Available.EmbeddingRedundancy},
				{"artistConcentration", w.Scores.ArtistConcentration, g.Scores.ArtistConcentration, w.Available.ArtistConcentration, g.Available.ArtistConcentration},
				{"albumConcentration", w.Scores.AlbumConcentration, g.Scores.AlbumConcentration, w.Available.AlbumConcentration, g.Available.AlbumConcentration},
				{"mmr", w.Scores.MMR, g.Scores.MMR, w.Available.MMR, g.Available.MMR},
			} {
				if f.wa != f.ga {
					t.Fatalf("trial %d pos %d %s: available %v != reference %v", trial, i, f.name, f.ga, f.wa)
				}
				if math.Abs(f.w-f.g) > 1e-12 {
					t.Fatalf("trial %d pos %d %s: %.17g != reference %.17g", trial, i, f.name, f.g, f.w)
				}
			}
		}
	}
}
