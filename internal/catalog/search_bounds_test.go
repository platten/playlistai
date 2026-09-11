package catalog

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func BenchmarkResolveCommonToken(b *testing.B) {
	c, err := Open("testdata")
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Resolve("a", 5)
	}
}

func TestResolveKeepsLateExactMatchesAndStableTiers(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE tracks(row INTEGER,id TEXT,artist TEXT,title TEXT,search TEXT);
	INSERT INTO tracks VALUES
	(0,'broad','Other','Needle song','other needle song'),
	(1,'artist-prefix','Needle Band','Song','needle band song'),
	(2,'artist-exact','Needle','Song','needle song'),
	(3,'title-exact','Other','Needle','other needle'),
	(4,'title-exact-second','Another','Needle','another needle')`); err != nil {
		t.Fatal(err)
	}
	c := &Catalog{db: db, resolveMax: 200}
	for limit, want := range map[int][]string{2: {"title-exact", "title-exact-second"}, 4: {"title-exact", "title-exact-second", "artist-exact", "artist-prefix"}, 5: {"title-exact", "title-exact-second", "artist-exact", "artist-prefix", "broad"}} {
		var got []string
		for _, ref := range c.Resolve("needle", limit) {
			got = append(got, ref.ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limit %d: %v want %v", limit, got, want)
		}
	}
}

func TestRepresentativeCacheBoundAndOversizedQuery(t *testing.T) {
	c := openTestdata(t)
	for i := 0; i < resolutionCacheLimit; i++ {
		c.representativeCache[fmt.Sprint(i)] = nil
	}
	if got := c.artistRepresentatives("Justice"); len(got) == 0 {
		t.Fatal("artist representatives lost")
	}
	if len(c.representativeCache) > resolutionCacheLimit {
		t.Fatal("representative cache exceeds limit")
	}
	c.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, TrackID: strings.Repeat("x", resolutionCacheKeyLimit+1)})
	for key := range c.resolutionCache {
		if len(key) > resolutionCacheKeyLimit {
			t.Fatal("retained oversized key")
		}
	}
}

func TestResolutionCacheBoundPreservesResults(t *testing.T) {
	c := openTestdata(t)
	ref := core.IntentReference{Kind: core.ReferenceTrack, TrackID: "seed0001"}
	before := c.ResolveReference(ref)
	for i := 0; i < 2200; i++ {
		c.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, TrackID: fmt.Sprintf("missing-%d", i)})
	}
	if len(c.resolutionCache) > 1024 {
		t.Fatalf("unbounded cache: %d", len(c.resolutionCache))
	}
	after := c.ResolveReference(ref)
	if before.Selected == nil || after.Selected == nil || before.Selected.EntityID != after.Selected.EntityID {
		t.Fatal("eviction changed identity")
	}
}
