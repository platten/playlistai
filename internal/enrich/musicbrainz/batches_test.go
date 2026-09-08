package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestLargerMusicBrainzArtistBatchesCacheAndReuseConnection(t *testing.T) {
	var calls, connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("limit") != "100" {
			t.Error("batch not full-sized")
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var artists []map[string]string
		for i := offset; i < min(offset+100, 350); i++ {
			artists = append(artists, map[string]string{"id": fmt.Sprint(i), "name": fmt.Sprintf("Artist %d", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 350, "artists": artists})
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	c := newClient(t, server.URL, time.Nanosecond)
	for range 2 {
		pool := c.genreArtists(context.Background(), "ambient")
		if len(pool.Artists) != 300 || pool.Complete {
			t.Fatalf("bounded artist batch: %d complete=%v", len(pool.Artists), pool.Complete)
		}
	}
	if calls.Load() != 3 || connections.Load() != 1 {
		t.Fatalf("want 3 requests on 1 connection, got %d on %d", calls.Load(), connections.Load())
	}
}

func TestMusicBrainzRecordingPagesDrainBufferBeforeFetching(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/ws/2/artist" {
			_, _ = fmt.Fprint(w, `{"count":1,"artists":[{"id":"a","name":"Artist"}]}`)
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var recordings []map[string]any
		for i := offset; i < min(offset+100, 101); i++ {
			title := "Absent"
			if i == 0 {
				title = "One"
			}
			if i == 1 {
				title = "Two"
			}
			if i == 100 {
				title = "Later"
			}
			recordings = append(recordings, map[string]any{"id": fmt.Sprint(i), "title": title, "artist-credit": []map[string]any{{"name": "Artist", "artist": map[string]string{"id": "a"}}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 101, "recordings": recordings})
	}))
	defer server.Close()
	c := newClient(t, server.URL, time.Nanosecond)
	cat := &artistRecordingFixture{Catalog: fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"}, fakes.CatalogTrack{ID: "two", Display: "Artist - Two"}, fakes.CatalogTrack{ID: "later", Display: "Artist - Later"})}
	intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}}}
	var first []core.TrackRef
	for run := 0; run < 2; run++ {
		stream := c.OpenCandidates(intent, cat, cat)
		var tracks []core.TrackRef
		for i := 0; i < 3; i++ {
			track, err := stream.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			tracks = append(tracks, track)
			if run == 0 && i < 2 && calls.Load() != 2 {
				t.Fatal("fetched a continuation before draining buffered tracks")
			}
		}
		if tracks[2].ID != "later" {
			t.Fatal("later recording page not visited")
		}
		if _, err := stream.Next(context.Background()); err != io.EOF {
			t.Fatal(err)
		}
		if run == 0 {
			first = tracks
		} else if !reflect.DeepEqual(first, tracks) {
			t.Fatal("cached iteration not deterministic")
		}
	}
	if calls.Load() != 3 || cat.reads != 2 {
		t.Fatalf("cache/resolver reuse: requests=%d catalog scans=%d", calls.Load(), cat.reads)
	}
}

func TestDiscogsLargePagesLazyIterationAndCrossPageDedup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			calls++
			if r.URL.Path == "/database/search" {
				if r.URL.Query().Get("per_page") != "100" {
					t.Error("small search page")
				}
				if r.URL.Query().Get("page") == "1" {
					return jsonResponse(r, 200, `{"pagination":{"items":101,"pages":2},"results":[{"id":1,"type":"release"}]}`)
				}
				return jsonResponse(r, 200, `{"pagination":{"items":101,"pages":2},"results":[{"id":1,"type":"release"},{"id":2,"type":"release"}]}`)
			}
			if r.URL.Path == "/releases/1" {
				return jsonResponse(r, 200, `{"id":1,"artists":[{"name":"Artist"}],"tracklist":[{"type_":"track","title":"One"},{"type_":"track","title":"Two"}]}`)
			}
			return jsonResponse(r, 200, `{"id":2,"artists":[{"name":"Artist"}],"tracklist":[{"type_":"track","title":"Later"}]}`)
		})
		c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return jsonResponse(r, 403, `{}`), nil })}
		cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"}, fakes.CatalogTrack{ID: "two", Display: "Artist - Two"}, fakes.CatalogTrack{ID: "later", Display: "Artist - Later"})
		intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}}}
		for run := 0; run < 2; run++ {
			stream := c.OpenCandidates(intent, cat, cat)
			for i := 0; i < 3; i++ {
				track, err := stream.Next(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if i == 2 && track.ID != "later" {
					t.Fatal("missed later search page")
				}
				if run == 0 && i < 2 && calls != 2 {
					t.Fatal("eagerly fetched more pages")
				}
			}
			if _, err := stream.Next(context.Background()); err != io.EOF {
				t.Fatal(err)
			}
		}
		if calls != 4 {
			t.Fatalf("duplicate release/page not cached: %d calls", calls)
		}
	})
}

func TestDiscogsPaginationBoundsRepeatedResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		searches, releases := 0, 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			if r.URL.Path == "/database/search" {
				searches++
				return jsonResponse(r, 200, `{"pagination":{"items":10000,"pages":100},"results":[{"id":1,"type":"release"}]}`)
			}
			releases++
			return jsonResponse(r, 200, `{"id":1,"artists":[],"tracklist":[]}`)
		})
		c.hc = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return jsonResponse(r, 403, `{}`), nil })}
		cat := fakes.NewCatalog(2)
		intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}}}
		stream := c.OpenCandidates(intent, cat, cat)
		if _, err := stream.Next(context.Background()); err != io.EOF {
			t.Fatal(err)
		}
		if searches != 3 || releases != 1 {
			t.Fatalf("unbounded/redundant pagination: %d searches, %d releases", searches, releases)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := stream.Next(ctx); err != context.Canceled {
			t.Fatal("cancellation ignored")
		}
	})
}
