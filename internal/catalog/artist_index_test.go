package catalog

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestArtistRowIndexMatchesLegacyQuery(t *testing.T) {
	c := openTestdata(t)
	index := c.artistRows
	for _, artist := range []string{"Justice", "Björk", "missing"} {
		indexed, err := c.ArtistRecordings(context.Background(), artist)
		if err != nil {
			t.Fatal(err)
		}
		c.artistRows = nil
		legacy, err := c.ArtistRecordings(context.Background(), artist)
		c.artistRows = index
		if err != nil || !reflect.DeepEqual(indexed, legacy) {
			t.Fatalf("index mismatch for %s: %v", artist, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ArtistRecordings(ctx, "Justice"); err != context.Canceled {
		t.Fatal(err)
	}
}

func BenchmarkArtistRecordings(b *testing.B) {
	dir := os.Getenv("PLAYLISTAI_BENCH_CATALOG")
	if dir == "" {
		b.Skip("PLAYLISTAI_BENCH_CATALOG not set")
	}
	c, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close() })
	index := c.artistRows
	for _, name := range []string{"legacy_scan", "artist_rows"} {
		c.artistRows = index
		if name == "legacy_scan" {
			c.artistRows = nil
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := c.ArtistRecordings(context.Background(), "Radiohead"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestArtistRowsSpanMultipleBatchesInCatalogOrder(t *testing.T) {
	c := metadataResolverCatalog(t)
	c.artistRows = map[string][]int{}
	tx := resolverFixtureTransaction(t, c.db)
	for row := 0; row < 600; row++ {
		insertResolverTrack(t, tx, row, fmt.Sprint(row), "Artist", "Song")
		c.artistRows["Artist"] = append(c.artistRows["Artist"], row)
	}
	commitResolverFixture(t, tx)
	tracks, err := c.ArtistRecordings(context.Background(), "Artist")
	if err != nil || len(tracks) != 600 {
		t.Fatalf("batched artist lookup: %d %v", len(tracks), err)
	}
	for row, track := range tracks {
		if track.ID != fmt.Sprint(row) {
			t.Fatal("catalog order changed across batches")
		}
	}
}
