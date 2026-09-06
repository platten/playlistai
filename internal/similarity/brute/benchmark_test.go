package brute_test

import (
	"context"
	"os"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// BenchmarkCatalogSearch is opt-in because the production catalog is a local
// downloaded artifact, not repository test data. It provides the exact-search
// baseline used by the recommendation optimization milestone.
//
//	PLAYLISTAI_BENCH_CATALOG=/path/to/catalog go test \
//	  ./internal/similarity/brute -run '^$' -bench BenchmarkCatalogSearch \
//	  -benchtime=10x -benchmem
func BenchmarkCatalogSearch(b *testing.B) {
	dir := os.Getenv("PLAYLISTAI_BENCH_CATALOG")
	if dir == "" {
		b.Skip("PLAYLISTAI_BENCH_CATALOG is not set")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cat.Close() })
	vectors, ok := cat.Vectors(cat.ID(cat.Len() / 2))
	if !ok {
		b.Fatal("benchmark query track missing")
	}
	query := ports.SimilarityQuery{AudioSum: vectors.Audio, TrackSum: vectors.Track, Weights: [2]float32{0.5, 0.5}, K: 64}
	for _, tc := range []struct {
		name    string
		workers int
	}{{"serial", 1}, {"parallel", 0}} {
		b.Run(tc.name, func(b *testing.B) {
			engine := brute.NewWithWorkers(cat, tc.workers)
			b.ReportMetric(float64(cat.Len()), "tracks")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := engine.Search(context.Background(), query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCatalogEngineBuild measures the exact engine's only derived state:
// two float32 inverse norms per catalog row. It builds no persistent index.
func BenchmarkCatalogEngineBuild(b *testing.B) {
	dir := os.Getenv("PLAYLISTAI_BENCH_CATALOG")
	if dir == "" {
		b.Skip("PLAYLISTAI_BENCH_CATALOG is not set")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cat.Close() })
	b.ReportMetric(float64(cat.Len()*2*4), "derived-bytes")
	b.ReportMetric(float64(cat.Len()), "tracks")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = brute.New(cat)
	}
}
