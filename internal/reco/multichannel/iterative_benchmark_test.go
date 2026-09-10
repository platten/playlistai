package multichannel_test

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// No network/model work: explicit references use the desktop's iterative
// recommendation path, with all catalog retrieval, selection and sequencing.
type referenceDiscovery struct{}

type uncachedSearch struct{ ports.SimilarityEngine }

func (referenceDiscovery) OpenCandidates(core.MusicIntent, ports.Catalog, ports.ReferenceResolver) ports.MusicCandidateStream {
	return nil
}

func BenchmarkCatalogIterativeGeneration(b *testing.B) {
	dir := os.Getenv("PLAYLISTAI_BENCH_CATALOG")
	if dir == "" {
		b.Skip("PLAYLISTAI_BENCH_CATALOG is not set")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cat.Close() })
	sim := brute.New(cat)
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion, VerificationPolicy: core.BestAvailable,
		Mode: core.ModeSimilar, Seed: "42",
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Boards of Canada", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{TotalTrackCount: 10, AudioWeight: .5, CooccurrenceWeight: .5, Discovery: .3, ArtistDiversity: .7, TransitionSmoothness: .6},
	}
	baseline := multichannel.New(cat, uncachedSearch{sim}, cat, multichannel.DefaultConfig()).WithCandidateSource(referenceDiscovery{})
	want, err := baseline.Build(context.Background(), intent)
	if err != nil || len(want.Tracks) != 10 {
		b.Fatalf("baseline tracks=%d err=%v", len(want.Tracks), err)
	}
	for _, tc := range []struct {
		name string
		sim  ports.SimilarityEngine
	}{{"uncached", uncachedSearch{sim}}, {"request_cache", sim}} {
		b.Run(tc.name, func(b *testing.B) {
			engine := multichannel.New(cat, tc.sim, cat, multichannel.DefaultConfig()).WithCandidateSource(referenceDiscovery{})
			got, err := engine.Build(context.Background(), intent)
			if err != nil || !reflect.DeepEqual(got, want) {
				b.Fatal("cached and uncached playlists/evidence differ", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				playlist, err := engine.Build(context.Background(), intent)
				if err != nil || len(playlist.Tracks) != 10 {
					b.Fatalf("tracks=%d err=%v", len(playlist.Tracks), err)
				}
			}
		})
	}
}
