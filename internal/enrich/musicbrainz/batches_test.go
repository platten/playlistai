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
