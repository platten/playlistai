package musicbrainz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestDiscoveryInputKeySeparatesReplayFromNewSampling(t *testing.T) {
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
	c := newClient(t, "http://localhost", time.Nanosecond)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient"}}}}
	s := c.OpenCandidates(intent, cat, cat).(*candidateStream)
	s.snapshot.Discovery = []core.TrackRef{{ID: "one"}}
	s.recordEvidence("one", "discogs_release_sample", "https://www.discogs.com/release/7")
	intent.Knowledge = s.Snapshot()
	if !c.OpenCandidates(intent, cat, cat).(*candidateStream).replay {
		t.Fatal("same inputs lost offline replay")
	}
	for _, change := range []func(*core.MusicIntent){
		func(m *core.MusicIntent) { m.Seed = "43" },
		func(m *core.MusicIntent) { m.Controls.Discovery = .9 },
		func(m *core.MusicIntent) { m.Controls.ArtistDiversity = .8 },
		func(m *core.MusicIntent) { m.VerificationPolicy = core.VerifiedOnly },
		func(m *core.MusicIntent) { m.Preferences.Genres = []core.IntentPreference{{Value: "rock"}} },
	} {
		m := intent
		change(&m)
		changed := c.OpenCandidates(m, cat, cat).(*candidateStream)
		if changed.replay || len(changed.snapshot.Discovery) != 0 {
			t.Fatal("changed inputs reused sampling")
		}
	}
	replay := c.OpenCandidates(intent, cat, cat).(*candidateStream)
	if _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evidence := replay.Evidence("one"); len(evidence) != 1 || evidence[0].Channel != "discogs_release_sample" {
		t.Fatalf("lost provider provenance: %+v", evidence)
	}
}

func TestDiscogsFullPagesReachLaterPageWithinDetailBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		searches, details := 0, 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			if r.URL.Path == "/database/search" {
				searches++
				if r.URL.Query().Get("page") == "1" {
					var entries []string
					for id := 1; id <= 100; id++ {
						entries = append(entries, fmt.Sprintf(`{"id":%d,"type":"release"}`, id))
					}
					return jsonResponse(r, 200, `{"pagination":{"items":101,"pages":2},"results":[`+strings.Join(entries, ",")+`]}`)
				}
				return jsonResponse(r, 200, `{"pagination":{"items":101,"pages":2},"results":[{"id":101,"type":"release"}]}`)
			}
			details++
			id := strings.TrimPrefix(r.URL.Path, "/releases/")
			tracks := "[]"
			if id == "101" {
				tracks = `[{"type_":"track","title":"One"}]`
			}
			return jsonResponse(r, 200, fmt.Sprintf(`{"id":%s,"artists":[{"name":"Artist"}],"tracklist":%s}`, id, tracks))
		})
		c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return jsonResponse(r, 403, `{}`), nil })}
		cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
		intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient"}}}}
		stream := c.OpenCandidates(intent, cat, cat)
		track, err := stream.Next(context.Background())
		if err != nil || track.ID != "one" || searches != 2 || details != 21 {
			t.Fatalf("later page: %+v %v searches=%d details=%d", track, err, searches, details)
		}
		if _, err := stream.Next(context.Background()); err != io.EOF {
			t.Fatal(err)
		}
	})
}

func TestDiscogsBufferWindowAvoidsUnnecessaryReleaseRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		details := 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			if r.URL.Path == "/database/search" {
				var entries []string
				for id := 1; id <= 20; id++ {
					entries = append(entries, fmt.Sprintf(`{"id":%d,"type":"release"}`, id))
				}
				return jsonResponse(r, 200, `{"pagination":{"items":20,"pages":1},"results":[`+strings.Join(entries, ",")+`]}`)
			}
			details++
			id := strings.TrimPrefix(r.URL.Path, "/releases/")
			return jsonResponse(r, 200, fmt.Sprintf(`{"id":%s,"artists":[{"name":"Artist"}],"tracklist":[{"type_":"track","title":"One"},{"type_":"track","title":"Two"}]}`, id))
		})
		cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"}, fakes.CatalogTrack{ID: "two", Display: "Artist - Two"})
		s := &candidateStream{client: c, cat: cat, resolver: cat, seen: map[string]bool{}, genres: []string{"ambient"}, fallback: &discogsCandidates{}}
		// Use OpenCandidates for deterministic RNG initialization.
		intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient"}}}}
		initialized := c.OpenCandidates(intent, cat, cat).(*candidateStream)
		s.rng = initialized.rng
		for range 2 {
			if _, err := s.Next(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if details != discoveryWindow {
			t.Fatalf("buffer ignored: %d detail requests", details)
		}
	})
}

func TestDiscogsCleanupIsThrottledWithoutServingExpiredCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := discogsFixture(t, func(r *http.Request) *http.Response { calls++; return jsonResponse(r, 200, `{"id":7,"tracklist":[]}`) })
		c.expireDiscogs(context.Background())
		deadline := c.nextDiscogsCleanup
		key := metadataKey(c.discogs.base, "/releases/7", "discogs-v1:")
		c.memory = map[string]cachedResponse{key: {body: `{"id":7,"tracklist":[]}`, fetched: time.Now().Add(-discogsTTL).Unix()}}
		for range 2 {
			if _, err := c.discogsRelease(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 || !c.nextDiscogsCleanup.Equal(deadline) {
			t.Fatal("cleanup rescanned or expired cache was served")
		}
	})
}
