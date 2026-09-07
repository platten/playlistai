package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestKnowledgeGraphBudgetCacheAndRecordingIdentity(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/ws/2/artist":
			_, _ = w.Write([]byte(`{"count":0,"artists":[]}`))
		case "/genres":
			_, _ = w.Write([]byte(`<a href="/genre/g1">Novel music</a><a href="/genre/g2">Related music</a>`))
		case "/genre/g1":
			_, _ = w.Write([]byte(`<table><tr><th>subgenre of:</th><td><a href="/genre/g2"><bdi>Related music</bdi></a></td></tr></table>`))
		case "/genre/g1/aliases":
			_, _ = w.Write([]byte(`<table class="tbl"><thead><tr><th>Alias</th></tr></thead><tbody><tr><td><bdi>Novel alias</bdi></td></tr></tbody></table>`))
		case "/ws/2/recording":
			_, _ = w.Write([]byte(`{"recordings":[{"id":"right","title":"One","artist-credit":[{"name":"Artist","artist":{"id":"artist"}}],"first-release-date":"1992","tags":[{"name":"Novel music","count":2}]},{"id":"wrong","title":"One live","artist-credit":[{"name":"Artist"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := New(Config{UserAgent: "fixture", MirrorURL: srv.URL, CachePath: filepath.Join(t.TempDir(), "cache.sqlite"), Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	intent := core.MusicIntent{Version: 8, Seed: core.NewRNGSeed(7), Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "Novel music"}}}}
	first, err := client.ResolveMusic(context.Background(), intent, cat, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Knowledge.Candidates) != 1 || first.Knowledge.Tracks[0].OriginalReleaseDate != "1992" || !first.Knowledge.Graph.Matches("Related music", "Novel alias") {
		t.Fatalf("bad knowledge: %+v", first.Knowledge)
	}
	count := calls
	if !client.IsCachedGenre(context.Background(), "Novel alias") || client.IsCachedGenre(context.Background(), "Unknown category") || calls != count {
		t.Fatal("cached genre lookup lost aliases or performed a network request")
	}
	again, err := client.ResolveMusic(context.Background(), intent, cat, cat, nil)
	if err != nil || calls != count || again.Knowledge.ID != first.Knowledge.ID {
		t.Fatal("cache or snapshot identity changed")
	}
	ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, &knowledgeBudget{requests: KnowledgeRequests})
	if _, err = client.knowledgeGet(ctx, "/uncached", false); err == nil || calls != count {
		t.Fatal("request ceiling bypassed")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = client.ResolveMusic(canceled, intent, cat, cat, nil); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestKnowledgeNegativeTTLAndOfflineStale(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"recordings":[]}`)) }))
	defer srv.Close()
	client, err := New(Config{UserAgent: "fixture", MirrorURL: srv.URL, CachePath: filepath.Join(t.TempDir(), "cache.sqlite"), Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	_, err = client.knowledgeGet(ctx, "/negative", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-48*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.knowledgeGet(ctx, "/negative", false); err != nil || calls != 2 {
		t.Fatal("negative response used the longer positive TTL")
	}
	_, err = client.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-40*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	offline := context.WithValue(ctx, cacheOnlyKey{}, true)
	if raw, err := client.knowledgeGet(offline, "/negative", false); err != nil || len(raw) == 0 || calls != 2 {
		t.Fatal("offline stale response lost")
	}
	if _, err := client.knowledgeGet(offline, "/missing", false); err == nil || calls != 2 {
		t.Fatal("offline miss performed network lookup")
	}
}
