package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

func TestCountedGenreUsesProviderEvidenceOnColdAndWarmCache(t *testing.T) {
	for _, genre := range []string{"Classical", "Gqom", "未知ジャンル", "Salsa"} {
		t.Run(genre, func(t *testing.T) {
			// Reviewed dictionary categories have a stable canonical spelling;
			// unknown categories retain the original provider/user wording.
			expectedGenre := musicconcepts.Canonical("genre", genre)
			artistLookups := 0
			firstSearch := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if firstSearch == "" && (r.URL.Path == "/ws/2/artist" || r.URL.Path == "/ws/2/recording") {
					firstSearch = r.URL.Path
				}
				switch r.URL.Path {
				case "/genres":
					_, _ = fmt.Fprintf(w, `<a href="/genre/g1"><bdi>%s</bdi></a>`, genre)
				case "/genre/g1", "/genre/g1/aliases":
					_, _ = w.Write([]byte(`<html></html>`))
				case "/ws/2/artist":
					if r.URL.Query().Get("query") != `tag:"`+expectedGenre+`"` {
						artistLookups++
					}
					_, _ = w.Write([]byte(`{"count":0,"artists":[]}`))
				case "/ws/2/recording":
					_, _ = fmt.Fprintf(w, `{"recordings":[{"id":"recording","title":"One","artist-credit":[{"name":"Artist","artist":{"id":"artist"}}],"tags":[{"name":%q,"count":3}]}]}`, genre)
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
			for range 2 {
				for _, prompt := range []string{genre + " 10 tracks", "Make a 10-song " + genre + " playlist."} {
					intent, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
					got, err := client.ResolveMusic(context.Background(), intent, cat, cat, nil)
					if err != nil || got.Controls.TotalTrackCount != 10 || len(got.References) != 0 || len(got.EssentialCriteria) != 1 || got.EssentialCriteria[0].Value != expectedGenre || len(got.Knowledge.Candidates) != 1 {
						t.Fatalf("counted genre failed: %+v, %v", got, err)
					}
				}
			}
			if artistLookups != 0 {
				t.Fatalf("genre was searched as artist %d times", artistLookups)
			}
			if firstSearch != "/ws/2/recording" {
				t.Fatal("artist sampling ran before genre recordings")
			}
		})
	}
}

func TestGenreRecordingSearchExpandsWithinBound(t *testing.T) {
	for _, match := range []bool{false, true} {
		t.Run(fmt.Sprint(match), func(t *testing.T) {
			var offsets []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				offsets = append(offsets, r.URL.Query().Get("offset"))
				title := "Absent"
				if match && len(offsets) == 2 {
					title = "One"
				}
				recordings := make([]map[string]any, 100)
				for i := range recordings {
					recordings[i] = map[string]any{"id": "recording", "title": title, "artist-credit": []map[string]any{{"name": "Artist", "artist": map[string]string{"id": "artist"}}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"count": 1000, "recordings": recordings})
			}))
			defer srv.Close()
			client, err := New(Config{UserAgent: "fixture", MirrorURL: srv.URL, Interval: time.Nanosecond})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			var snapshot core.KnowledgeSnapshot
			client.searchKnowledgeRecordings(context.Background(), `tag:"Classical"`, cat, cat, &snapshot, 1)
			want := 3
			if match {
				want = 2
			}
			if len(offsets) != want || offsets[0] != "" || offsets[1] != "100" {
				t.Fatalf("page budget/early stop: %v", offsets)
			}
			if match && len(snapshot.Candidates) != 1 {
				t.Fatal("later catalog match lost")
			}
		})
	}
}

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
	if _, err = client.knowledgeGet(ctx, "/negative", false); err != nil || calls != 1 {
		t.Fatal("negative response was not reused for one week")
	}
	_, err = client.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-40*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	offline := context.WithValue(ctx, cacheOnlyKey{}, true)
	if raw, err := client.knowledgeGet(offline, "/negative", false); err != nil || len(raw) == 0 || calls != 1 {
		t.Fatal("offline stale response lost")
	}
	if _, err := client.knowledgeGet(offline, "/missing", false); err == nil || calls != 1 {
		t.Fatal("offline miss performed network lookup")
	}
}
