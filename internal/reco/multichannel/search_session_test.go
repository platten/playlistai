package multichannel

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// Hide the optional factory to exercise the original, uncached exact path.
type withoutSearchSessions struct{ ports.SimilarityEngine }

func TestRequestSearchSessionPreservesPlaylist(t *testing.T) {
	cat := artistRequestCatalog()
	sim := brute.New(cat)
	optimized := New(cat, sim, cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	baseline := New(cat, withoutSearchSessions{sim}, cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	for _, mode := range []core.Mode{core.ModeSimilar, core.ModeJourney} {
		for _, weight := range []float64{0, .5, 1} {
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: mode, VerificationPolicy: core.BestAvailable, Seed: "42",
				References:  []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist 0", Influence: core.InfluencePositive}, {Kind: core.ReferenceArtist, Query: "Artist 1", Influence: core.InfluencePositive}},
				Controls:    core.IntentControls{TotalTrackCount: 10, AudioWeight: weight, CooccurrenceWeight: 1 - weight, Discovery: 1, ArtistDiversity: 1, TransitionSmoothness: .7},
				Constraints: core.IntentConstraints{ArtistsExclude: []string{"Artist 4"}},
			}
			if mode == core.ModeJourney {
				intent.Destination = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist 2", Influence: core.InfluencePositive}
			}
			want, err := baseline.Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			got, err := optimized.Build(context.Background(), intent)
			// Some fixture journeys exhaust the relevance floor early. Their
			// partial outcomes must be preserved just as exactly as full ones.
			if err != nil || len(got.Tracks) < 5 || !reflect.DeepEqual(got, want) {
				t.Fatalf("mode=%s weight=%g: tracks=%d baseline=%d equal=%v; cache changed tracks, scores, provenance or outcome: %v", mode, weight, len(got.Tracks), len(want.Tracks), reflect.DeepEqual(got, want), err)
			}
		}
	}
}
