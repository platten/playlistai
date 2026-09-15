package catalog_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

// Synthetic lookup cost only, not provider latency or startup loading time.
func BenchmarkDynamicCatalogResolve(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			base := fakes.NewCatalog(1)
			d, err := catalog.OpenDynamic(base, base, filepath.Join(b.TempDir(), "tracks.sqlite"))
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			for i := 0; i < count; i++ {
				if err := d.RegisterDynamicTrack(core.TrackMeta{Ref: core.TrackRef{ID: fmt.Sprintf("deezer:%06d", i), Artist: fmt.Sprintf("Artist %06d", i), Title: "Song"}, PreviewURL: "https://example.invalid/preview"}); err != nil {
					b.Fatal(err)
				}
			}
			query := fmt.Sprintf("Artist %06d", count-1)
			b.ReportAllocs()
			for b.Loop() {
				if got := d.Resolve(query, 20); len(got) != 1 {
					b.Fatalf("lookup returned %d matches", len(got))
				}
			}
		})
	}
}
