package musicbrainz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type artistRecordingFixture struct {
	*fakes.Catalog
	reads int
}

func (c *artistRecordingFixture) ArtistRecordings(ctx context.Context, artist string) ([]core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.reads++
	var out []core.TrackRef
	for row := 0; row < c.Len(); row++ {
		meta, _ := c.Meta(c.ID(row))
		if meta.Ref.Artist == artist {
			out = append(out, meta.Ref)
		}
	}
	return out, nil
}

func TestCandidateStreamRandomArtistRotationAndOfflineReplay(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/ws/2/artist" {
			if r.URL.Query().Get("query") != `tag:"未知ジャンル"` {
				t.Error("genre query altered")
			}
			_, _ = fmt.Fprint(w, `{"count":2,"artists":[{"id":"a","name":"A"},{"id":"b","name":"B"}]}`)
			return
		}
		id := strings.TrimPrefix(r.URL.Query().Get("query"), "arid:")
		_, _ = fmt.Fprintf(w, `{"recordings":[{"id":"%s1","title":"One","artist-credit":[{"name":"%s","artist":{"id":"%s"}}]},{"id":"%s2","title":"Two","artist-credit":[{"name":"%s","artist":{"id":"%s"}}]}]}`, id, strings.ToUpper(id), id, id, strings.ToUpper(id), id)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	cat := &artistRecordingFixture{Catalog: fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a1", Display: "A - One"}, fakes.CatalogTrack{ID: "a2", Display: "A - Two"}, fakes.CatalogTrack{ID: "b1", Display: "B - One"}, fakes.CatalogTrack{ID: "b2", Display: "B - Two"})}
	intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "未知ジャンル", Influence: core.InfluencePositive}}
	stream := client.OpenCandidates(intent, cat, cat)
	var first []core.TrackRef
	for {
		track, err := stream.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, track)
	}
	if len(first) != 4 || first[0].Artist == first[1].Artist {
		t.Fatalf("artist rotation: %+v", first)
	}
	if cat.reads != 2 {
		t.Fatalf("expected one catalog lookup per artist, got %d", cat.reads)
	}
	for _, track := range stream.Snapshot().Tracks {
		if len(track.GenreTags) != 0 {
			t.Fatal("artist genre fabricated track evidence")
		}
	}
	intent.Knowledge = stream.Snapshot()
	before := calls
	replay := client.OpenCandidates(intent, cat, cat)
	var second []core.TrackRef
	for {
		track, err := replay.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		second = append(second, track)
	}
	if calls != before || !reflect.DeepEqual(first, second) {
		t.Fatal("saved discovery consulted provider or reordered tracks")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replay.Next(ctx); err != context.Canceled {
		t.Fatal("cancellation ignored")
	}
}

func TestCompoundGenreDiscoveryRetainsEveryTag(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query"))
		if len(queries) == 1 {
			_, _ = fmt.Fprint(w, `{"count":0,"artists":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"count":1,"artists":[{"id":"a","name":"Artist"}]}`)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	pool := client.genreArtists(context.Background(), "ambient electronic")
	if len(pool.Artists) != 1 || len(queries) != 2 || queries[1] != `tag:"ambient" AND tag:"electronic"` {
		t.Fatalf("pool=%+v queries=%v", pool, queries)
	}
}
