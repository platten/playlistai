package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
)

func TestMissingArtistOnlineRecoveryGeneratesAndReplays(t *testing.T) {
	for _, found := range []bool{true, false} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.Contains(r.URL.Query().Encode(), "music+like") {
					t.Error("full prompt sent online")
				}
				switch r.URL.Path {
				case "/ws/2/artist":
					_, _ = fmt.Fprint(w, `{"count":1,"artists":[{"id":"justice","name":"Justice","aliases":[{"name":"Unlisted Alias"}]}]}`)
				case "/search/artist":
					_, _ = fmt.Fprint(w, `{"total":1,"data":[{"id":7,"name":"Justice"}]}`)
				case "/artist/7/top":
					if found {
						_, _ = fmt.Fprint(w, `{"data":[{"id":1,"title":"Not In This Catalog","artist":{"id":7,"name":"Justice"}},{"id":2,"title":"Genesis","artist":{"id":7,"name":"Justice"}}]}`)
					} else {
						_, _ = fmt.Fprint(w, `{"data":[]}`)
					}
				case "/ws/2/recording":
					_, _ = fmt.Fprint(w, `{"count":0,"recordings":[]}`)
				default:
					t.Errorf("unexpected lookup: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "fixture", MirrorURL: srv.URL, DeezerURL: srv.URL, Interval: time.Nanosecond, CachePath: filepath.Join(t.TempDir(), "metadata.sqlite")})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			container := newLoadedContainer(t)
			container.Knowledge = client
			api := New(container, nil)
			const prompt = "music like Unlisted Alias, 5 tracks"
			preview, err := api.ParseIntent(context.Background(), prompt)
			if err != nil || len(preview.ResolutionIssues) != 1 || calls.Load() != 0 {
				t.Fatalf("typing must stay offline: %+v %v calls=%d", preview, err, calls.Load())
			}
			generated, err := api.GenerateFromPromptWithContext(context.Background(), prompt, IntentSessionContext{GenerationID: "artist-recovery"})
			if err != nil {
				t.Fatal(err)
			}
			if generated.Playlist.GenerationID != "artist-recovery" || (len(generated.Playlist.Tracks) == 5) != found {
				t.Fatalf("unexpected result: %+v", generated.Playlist)
			}
			var notices []string
			for _, notice := range generated.Playlist.Notices {
				notices = append(notices, notice.Detail)
			}
			if !strings.Contains(strings.Join(notices, " "), "not found under that name") {
				t.Fatalf("artist-missing notice absent: %v", notices)
			}
			if !found {
				if generated.Playlist.Outcome.State != core.OutcomeNeedsClarification || !strings.Contains(strings.Join(notices, " "), "Could not find a verified catalog seed") {
					t.Fatal("miss lost actionable clarification")
				}
				return
			}
			if !strings.Contains(strings.Join(notices, " "), "Justice - Genesis") {
				t.Fatalf("chosen seed not disclosed: %v", notices)
			}
			before := calls.Load()
			rebuilt, err := api.BuildPlaylist(context.Background(), generated.Request)
			if err != nil || calls.Load() != before || len(rebuilt.Tracks) != 5 {
				t.Fatalf("rebuild failed or searched again: %v", err)
			}
			if generated.Request.Intent.References[0].Query != "Unlisted Alias" {
				t.Fatal("original requested artist was overwritten")
			}
		})
	}
}
