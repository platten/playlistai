package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

// No service or credential is needed for these transport/clock fixtures.
func discogsFixture(t *testing.T, handler func(*http.Request) *http.Response) *Client {
	t.Helper()
	d := &discogsClient{base: "https://api.discogs.com", token: "fixture-token", credentialPath: filepath.Join(t.TempDir(), "credentials", "discogs-token")}
	d.http = &http.Client{Transport: &discogsTransport{client: d, limiter: &discogsThrottle{gate: make(chan struct{}, 1)}, base: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Discogs token=fixture-token" || r.URL.Query().Has("token") {
			t.Error("credential transport incorrect")
		}
		return handler(r), nil
	})}}
	return &Client{base: "https://musicbrainz.org", ua: "PlaylistAI/test", discogs: d}
}

func jsonResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestDiscogsRateLimitSharedRetriesAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var starts []time.Time
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			starts = append(starts, time.Now())
			if len(starts) == 1 {
				return jsonResponse(r, 503, `{}`)
			}
			return jsonResponse(r, 200, `{"results":[],"pagination":{"items":0}}`)
		})
		for i := range 26 {
			if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {fmt.Sprint(i)}}); err != nil {
				t.Fatal(err)
			}
		}
		if len(starts) != 27 {
			t.Fatalf("retries not counted: %d", len(starts))
		}
		for i := 1; i < len(starts); i++ {
			if starts[i].Sub(starts[i-1]) < discogsInterval {
				t.Fatal("25/min spacing bypassed")
			}
		}
		if starts[25].Sub(starts[0]) < time.Minute {
			t.Fatal("more than 25 requests/minute")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if _, err := c.discogsSearch(ctx, url.Values{"genre": {"canceled"}}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if len(starts) != 27 {
			t.Fatal("canceled waiter dispatched")
		}
	})
}

func TestDiscogsRetryAfterSurvivesNextQueryAndCacheClear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var starts []time.Time
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			starts = append(starts, time.Now())
			response := jsonResponse(r, 200, `{"results":[]}`)
			if len(starts) == 1 {
				response.StatusCode = 429
				response.Header.Set("Retry-After", "120")
			}
			return response
		})
		if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"one"}}); err == nil {
			t.Fatal("429 accepted")
		}
		if err := c.ClearCache(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"two"}}); err != nil {
			t.Fatal(err)
		}
		if starts[1].Sub(starts[0]) < 120*time.Second {
			t.Fatal("Retry-After bypassed by next query/cache clear")
		}
	})
}

func TestDiscogsCacheFreshnessSanitizationAndDisable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, offline := 0, false
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			calls++
			if offline {
				return jsonResponse(r, 401, `{"message":"unauthorized"}`)
			}
			return jsonResponse(r, 200, `{"results":[],"images":["private"],"username":"unused"}`)
		})
		for range 2 {
			if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"ambient"}}); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Fatal("empty success not cached")
		}
		for _, entry := range c.memory {
			if strings.Contains(entry.body, "private") || strings.Contains(entry.body, "username") {
				t.Fatal("unneeded fields cached")
			}
		}
		time.Sleep(discogsTTL - time.Second)
		if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"ambient"}}); err != nil || calls != 1 {
			t.Fatal("fresh cache bypassed")
		}
		time.Sleep(time.Second)
		offline = true
		if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"ambient"}}); err == nil {
			t.Fatal("expired Discogs content served on outage")
		}
		if err := c.SetDiscogsToken(""); err != nil {
			t.Fatal(err)
		}
		before := calls
		if _, err := c.discogsSearch(context.Background(), url.Values{"genre": {"ambient"}}); err == nil || calls != before {
			t.Fatal("disabled fallback fetched")
		}
	})
}

func TestDiscogsCredentialPersistenceAndEndpointSafety(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "discogs-token")
	d, err := newDiscogs("", path)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{discogs: d}
	if err = c.SetDiscogsToken("fixture-token"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatal("credential permissions too broad")
	}
	d, err = newDiscogs("", path)
	if err != nil {
		t.Fatal(err)
	}
	c.discogs = d
	if !c.MetadataStatus().DiscogsConfigured {
		t.Fatal("token not loaded")
	}
	if err = c.SetDiscogsToken("bad\r\nHeader: value"); err == nil {
		t.Fatal("header injection allowed")
	}
	if err = c.ClearCache(context.Background()); err != nil || !c.MetadataStatus().DiscogsConfigured {
		t.Fatal("clearing queries deleted token")
	}
	if err = c.SetDiscogsToken(""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("token not removed")
	}
	if _, err = newDiscogs("https://other.example", path); err == nil {
		t.Fatal("credential endpoint override allowed")
	}
	d, err = newDiscogs("", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.http.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("credential redirect allowed")
	}
	other, err := newDiscogs("", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.http.Transport.(*discogsTransport).limiter != other.http.Transport.(*discogsTransport).limiter {
		t.Fatal("different clients bypass shared rate limit")
	}
}

func TestDiscogsFallbackCatalogIdentityExclusionsAndReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			calls++
			if r.URL.Path == "/database/search" {
				if r.URL.Query().Get("genre") != "ambient electronica" {
					t.Error("genre phrase changed")
				}
				return jsonResponse(r, 200, `{"results":[{"id":1,"type":"release"}],"pagination":{"items":1}}`)
			}
			return jsonResponse(r, 200, `{"id":1,"title":"Album","genres":["Electronic"],"artists":[{"id":2,"name":"Artist"}],"tracklist":[{"type_":"track","title":"One"},{"type_":"track","title":"Absent"},{"type_":"track","title":"Blocked","artists":[{"name":"Excluded"}]},{"type_":"heading","title":"Heading"}]}`)
		})
		mbCalls := 0
		c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { mbCalls++; return jsonResponse(r, 502, `{}`), nil })}
		cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"}, fakes.CatalogTrack{ID: "blocked", Display: "Excluded - Blocked"}, fakes.CatalogTrack{ID: "heading", Display: "Artist - Heading"})
		intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient electronica", Influence: core.InfluencePositive}}}}
		intent.Constraints.ArtistsExclude = []string{"Excluded"}
		stream := c.OpenCandidates(intent, cat, cat)
		track, err := stream.Next(context.Background())
		if err != nil || track.ID != "one" {
			t.Fatalf("fallback: %+v %v", track, err)
		}
		if _, err = stream.Next(context.Background()); err != io.EOF {
			t.Fatal(err)
		}
		snapshot := stream.Snapshot()
		if len(snapshot.Tracks) != 0 || len(snapshot.Sources) != 1 || snapshot.Sources[0] != discogsSource(1) {
			t.Fatal("release genres/IDs forged recording evidence or source lost")
		}
		intent.Knowledge = snapshot
		replay := c.OpenCandidates(intent, cat, cat)
		got, err := replay.Next(context.Background())
		if err != nil || !reflect.DeepEqual(got, track) || calls != 2 || mbCalls != 1 {
			t.Fatal("replay fetched/reordered")
		}
	})
}

func TestDiscogsNotUsedForHealthyEmptyMusicBrainz(t *testing.T) {
	c := discogsFixture(t, func(r *http.Request) *http.Response {
		t.Error("unnecessary Discogs lookup")
		return jsonResponse(r, 200, `{"results":[]}`)
	})
	c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(r, 200, `{"count":0,"artists":[]}`), nil
	})}
	cat := fakes.NewCatalog(2)
	intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}}}
	if _, err := c.OpenCandidates(intent, cat, cat).Next(context.Background()); err != io.EOF {
		t.Fatal(err)
	}
}

func TestDiscogsArtistAndAlbumReferenceFallback(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			c := discogsFixture(t, func(r *http.Request) *http.Response {
				if r.URL.Path == "/database/search" {
					items := 1
					if ambiguous {
						items = 20
					}
					return jsonResponse(r, 200, fmt.Sprintf(`{"pagination":{"items":%d},"results":[{"id":1,"type":"release"}]}`, items))
				}
				return jsonResponse(r, 200, `{"id":1,"master_id":4,"title":"Album","artists":[{"id":2,"name":"Artist"}],"tracklist":[{"type_":"track","title":"One"}]}`)
			})
			cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
			var snapshot core.KnowledgeSnapshot
			ref := core.IntentReference{Kind: core.ReferenceAlbum, Query: "Artist - Album"}
			got, ok := c.resolveDiscogsAlbum(context.Background(), ref, cat, cat, &snapshot)
			if !ok || got.Resolution == nil {
				t.Fatal("album fallback failed")
			}
			want := core.ResolutionResolved
			if ambiguous {
				want = core.ResolutionAmbiguous
			}
			if got.Resolution.Status != want {
				t.Fatalf("got %+v, want %s", got.Resolution, want)
			}
			if ambiguous && got.TrackID != "" {
				t.Fatal("incomplete album search selected a track")
			}
			if !ambiguous {
				if got.TrackID != "one" {
					t.Fatal("album track lost")
				}
				ref = core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist"}
				got, ok = c.discogsArtistSeed(context.Background(), ref, cat, cat, &snapshot)
				if !ok || got.TrackID != "one" {
					t.Fatal("artist fallback failed")
				}
			}
			if len(snapshot.Tracks) != 0 {
				t.Fatal("Discogs identities turned into MusicBrainz IDs")
			}
		})
	}
}
