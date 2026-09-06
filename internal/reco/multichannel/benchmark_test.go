package multichannel_test

import (
	"context"
	"os"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// BenchmarkCatalogGeneration is opt-in because the production catalog is a
// downloaded local artifact. It exercises resolution, multi-channel retrieval,
// ranking, selection, and sequencing with a deterministic intent.
func BenchmarkCatalogGeneration(b *testing.B) {
	dir := os.Getenv("PLAYLISTAI_BENCH_CATALOG")
	if dir == "" {
		b.Skip("PLAYLISTAI_BENCH_CATALOG is not set")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cat.Close() })
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion,
		Mode:    core.ModeSimilar,
		Seed:    "18446744073709551615",
		References: []core.IntentReference{{
			Kind: core.ReferenceArtist, Query: "Boards of Canada", Influence: core.InfluencePositive,
		}},
		Controls: core.IntentControls{TotalTrackCount: 20, AudioWeight: .5, CooccurrenceWeight: .5, Discovery: .3, ArtistDiversity: .7, TransitionSmoothness: .6},
	}
	for _, tc := range []struct {
		name    string
		workers int
	}{{"serial", 1}, {"parallel", 0}} {
		b.Run(tc.name, func(b *testing.B) {
			engine := multichannel.New(cat, brute.NewWithWorkers(cat, tc.workers), cat, multichannel.DefaultConfig())
			b.ReportMetric(float64(cat.Len()), "tracks")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := engine.Build(context.Background(), intent); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
