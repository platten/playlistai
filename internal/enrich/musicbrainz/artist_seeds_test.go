package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/resolution"
)

func seedTestCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "match", Display: "Canonical Artist - Second song", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "cover", Display: "Unrelated Artist - Popular song", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "version", Display: "Canonical Artist - Versioned song (Live)", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)
}

func seedTestIntent() core.MusicIntent {
	return core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "music like Missing Alias", VerificationPolicy: core.BestAvailable,
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Missing Alias", Influence: core.InfluencePositive}},
	}.Normalized()
}

func seedTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); handler(w, r) }))
	t.Cleanup(srv.Close)
	c, err := New(Config{UserAgent: "PlaylistAI fixture", MirrorURL: srv.URL, DeezerURL: srv.URL, CachePath: filepath.Join(t.TempDir(), "metadata.sqlite"), Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, &calls
}

func seedArtistResponse(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/ws/2/artist":
		_, _ = fmt.Fprint(w, `{"count":1,"artists":[{"id":"mb-artist","name":"Canonical Artist","aliases":[{"name":"Missing Alias"}]}]}`)
	case "/search/artist":
		_, _ = fmt.Fprint(w, `{"total":2,"data":[{"id":99,"name":"Wrong First Result"},{"id":7,"name":"Canonical Artist"}]}`)
	default:
		return false
	}
	return true
}

func TestMissingArtistTriesPopularTracksInOrderAndCachesIdentity(t *testing.T) {
	c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if seedArtistResponse(w, r) {
			return
		}
		if r.URL.Path != "/artist/7/top" {
			t.Errorf("unexpected lookup: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("index") == "0" {
			// Neither a same-title cover nor a different recording version may seed.
			_, _ = fmt.Fprint(w, `{"data":[{"id":1,"title":"Popular song","artist":{"id":7,"name":"Canonical Artist"}},{"id":2,"title":"Versioned song","artist":{"id":7,"name":"Canonical Artist"}}],"next":"https://untrusted.example/never-follow"}`)
		} else {
			_, _ = fmt.Fprint(w, `{"data":[{"id":3,"title":"Second song","artist":{"id":7,"name":"Canonical Artist"}}]}`)
		}
	})
	cat := seedTestCatalog()
	progress := &fakes.RecordingProgress{}
	intent := seedTestIntent()
	intent.Journey.Waypoints = append([]core.IntentReference(nil), intent.References...)
	destination := intent.References[0]
	intent.Destination = &destination
	got, err := c.ResolveMusic(context.Background(), intent, cat, cat, progress)
	if err != nil {
		t.Fatal(err)
	}
	if got.References[0].TrackID != "match" || got.Journey.Waypoints[0].TrackID != "match" || got.References[0].Query != "Missing Alias" {
		t.Fatalf("bad recovered references: %+v", got)
	}
	if got.Destination.TrackID != "match" || got.Destination.Resolution.Selected.Evidence[0].Match != "online_artist_recording" {
		t.Fatal("destination lost recovered identity evidence")
	}
	if calls.Load() != 4 {
		t.Fatalf("lookups duplicated across references: %d", calls.Load())
	}
	_, issues := resolution.Apply(cat, got)
	if len(issues) != 0 {
		t.Fatalf("recovered identity was discarded: %+v", issues)
	}
	notices := strings.Join(got.Knowledge.Notices, " ")
	if !strings.Contains(notices, "not found under that name") || !strings.Contains(notices, "after checking 3 recording(s)") {
		t.Fatal(notices)
	}
	before := calls.Load()
	again, err := c.ResolveMusic(context.Background(), intent, cat, cat, nil)
	if err != nil || calls.Load() != before || again.Knowledge.ID != got.Knowledge.ID {
		t.Fatalf("cached evidence not reused: %v", err)
	}
	encoded, _ := json.Marshal(got)
	var replay core.MusicIntent
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatal(err)
	}
	replayed, err := c.ResolveMusic(context.Background(), replay, cat, cat, nil)
	if err != nil || calls.Load() != before || replayed.References[0].TrackID != "match" {
		t.Fatal("history replay looked up new evidence")
	}
}

func TestMissingArtistFallsBackToOtherRecordingsAndExplainsMisses(t *testing.T) {
	for _, found := range []bool{true, false} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			c, _ := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if seedArtistResponse(w, r) {
					return
				}
				switch r.URL.Path {
				case "/artist/7/top":
					_, _ = fmt.Fprint(w, `{"data":[]}`)
				case "/ws/2/recording":
					if r.URL.Query().Get("query") != "arid:mb-artist" {
						t.Errorf("recordings not scoped to identified artist: %s", r.URL)
					}
					if found {
						_, _ = fmt.Fprint(w, `{"count":1,"recordings":[{"id":"recording","title":"Second song","artist-credit":[{"name":"Canonical Artist","artist":{"id":"mb-artist"}}]}]}`)
					} else {
						_, _ = fmt.Fprint(w, `{"count":0,"recordings":[]}`)
					}
				default:
					http.NotFound(w, r)
				}
			})
			cat := seedTestCatalog()
			got, err := c.ResolveMusic(context.Background(), seedTestIntent(), cat, cat, nil)
			if err != nil || (got.References[0].TrackID != "") != found {
				t.Fatalf("found=%v, result=%+v, err=%v", found, got.References, err)
			}
			notices := strings.Join(got.Knowledge.Notices, " ")
			if !strings.Contains(notices, "order does not indicate popularity") {
				t.Fatal(notices)
			}
			if !found && !strings.Contains(notices, "Could not find a verified catalog seed") {
				t.Fatal(notices)
			}
		})
	}
}

func TestMissingArtistAmbiguityCancellationAndLookupBudget(t *testing.T) {
	c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/artist" {
			t.Errorf("ambiguous artist should not reach top tracks: %s", r.URL)
		}
		_, _ = fmt.Fprint(w, `{"count":2,"artists":[{"id":"one","name":"Missing Alias"},{"id":"two","name":"Missing Alias"}]}`)
	})
	cat := seedTestCatalog()
	got, err := c.ResolveMusic(context.Background(), seedTestIntent(), cat, cat, nil)
	if err != nil || got.References[0].TrackID != "" || !strings.Contains(strings.Join(got.Knowledge.Notices, " "), "multiple artists") {
		t.Fatalf("ambiguity guessed: %+v %v", got, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected extra lookup")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ResolveMusic(ctx, seedTestIntent(), cat, cat, nil); err == nil || calls.Load() != 1 {
		t.Fatal("cancellation ignored")
	}
	ctx = context.WithValue(context.Background(), knowledgeBudgetKey{}, &knowledgeBudget{requests: KnowledgeRequests})
	var out any
	if err := c.deezerSeedGet(ctx, "/uncached", &out, &core.KnowledgeSnapshot{}); err == nil || calls.Load() != 1 {
		t.Fatal("Deezer bypassed shared metadata request budget")
	}
}

func TestArtistSeedLookupSkipsLocalAndNegativeReferences(t *testing.T) {
	c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected online lookup: %s", r.URL)
		http.NotFound(w, r)
	})
	cat := seedTestCatalog()
	intent := seedTestIntent()
	intent.References[0].Influence = core.InfluenceNegative
	intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: "Canonical Artist", Influence: core.InfluencePositive})
	if _, err := c.ResolveMusic(context.Background(), intent, cat, cat, nil); err != nil || calls.Load() != 0 {
		t.Fatal("local/negative reference triggered online recovery")
	}
}

func TestArtistSeedProviderErrorIsRetryableAndTopTracksAreBounded(t *testing.T) {
	var attempts atomic.Int32
	c, _ := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/artist" && attempts.Add(1) == 1 {
			_, _ = fmt.Fprint(w, `{"error":{"message":"Temporary outage"}}`)
			return
		}
		if seedArtistResponse(w, r) {
			return
		}
		if r.URL.Path != "/artist/7/top" {
			http.NotFound(w, r)
			return
		}
		data := make([]deezerSeedTrack, 101)
		for i := range data {
			data[i] = deezerSeedTrack{ID: int64(i + 1), Title: "Missing", Artist: deezerSeedArtist{ID: 7, Name: "Canonical Artist"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "next": "anything"})
	})
	artist := seedArtist{ID: "mb-artist", Name: "Canonical Artist"}
	checked := 0
	selectTrack := func(string, []string, string, string) bool { checked++; return false }
	if _, err := c.tryPopularArtistTracks(context.Background(), artist, &core.KnowledgeSnapshot{}, selectTrack); err == nil {
		t.Fatal("provider error accepted")
	}
	if _, err := c.tryPopularArtistTracks(context.Background(), artist, &core.KnowledgeSnapshot{}, selectTrack); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || checked != artistSeedTrackLimit {
		t.Fatalf("error cached or top-track limit ignored: attempts=%d checked=%d", attempts.Load(), checked)
	}
}

func TestMusicBrainzBackoffDoesNotBlockDeezerRecovery(t *testing.T) {
	c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/artist":
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/search/artist":
			_, _ = fmt.Fprint(w, `{"total":1,"data":[{"id":7,"name":"Missing Alias"}]}`)
		case "/artist/7/top":
			_, _ = fmt.Fprint(w, `{"data":[{"id":3,"title":"Second song","artist":{"id":7,"name":"Canonical Artist"}}]}`)
		default:
			t.Errorf("unexpected lookup: %s", r.URL)
			http.NotFound(w, r)
		}
	})
	cat := seedTestCatalog()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := c.ResolveMusic(ctx, seedTestIntent(), cat, cat, nil)
	if err != nil || got.References[0].TrackID != "match" || calls.Load() != 3 {
		t.Fatalf("MusicBrainz outage blocked independent fallback: %+v, %v, calls=%d", got.References, err, calls.Load())
	}
}
