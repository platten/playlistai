package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestGenreArtistPoolPaginationSamplingAndCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("limit") != "100" {
			t.Error("request did not ask for 100 results")
		}
		if r.URL.Path == "/ws/2/artist" {
			if r.URL.Query().Get("query") != `tag:"unfamiliar genre"` {
				t.Error("genre query lost")
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			var artists []map[string]any
			for i := offset; i < min(offset+60, 120); i++ {
				artists = append(artists, map[string]any{"id": fmt.Sprint(i), "name": fmt.Sprintf("Artist %03d", i), "tags": []map[string]any{{"name": "unfamiliar genre", "count": 2}}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 120, "artists": artists})
			return
		}
		id := strings.TrimPrefix(r.URL.Query().Get("query"), "arid:")
		n, _ := strconv.Atoi(id)
		var recordings []map[string]any
		for j := 0; j < 6; j++ {
			recordings = append(recordings, map[string]any{"id": fmt.Sprintf("%s-%d", id, j), "title": fmt.Sprintf("Track %d", j), "artist-credit": []map[string]any{{"name": fmt.Sprintf("Artist %03d", n), "artist": map[string]any{"id": id}}}})
		}
		// A first hit with a mismatching artist MBID must not be sampled.
		recordings[0]["artist-credit"] = []map[string]any{{"name": fmt.Sprintf("Artist %03d", n), "artist": map[string]any{"id": "wrong"}}}
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": recordings})
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, time.Nanosecond)
	var tracks []fakes.CatalogTrack
	for i := 0; i < 120; i++ {
		for j := 0; j < 6; j++ {
			tracks = append(tracks, fakes.CatalogTrack{ID: fmt.Sprintf("%d-%d", i, j), Display: fmt.Sprintf("Artist %03d - Track %d", i, j), Audio: []float32{1, 0}, Track: []float32{1, 0}})
		}
	}
	cat := fakes.NewCatalog(2, tracks...)
	run := func(seed uint64) core.KnowledgeSnapshot {
		intent := core.MusicIntent{Version: 8, Seed: core.NewRNGSeed(seed), Constraints: core.IntentConstraints{ArtistsExclude: []string{"Artist 000"}}}
		snapshot := core.KnowledgeSnapshot{}
		if err := c.sampleGenreArtists(context.Background(), &intent, []string{"unfamiliar genre", "unfamiliar genre"}, cat, cat, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	first := run(42)
	if len(first.ArtistPools) != 1 || len(first.ArtistPools[0].Artists) != 120 || len(first.ArtistPools[0].SampledArtists) != 3 || len(first.Candidates) != 9 {
		t.Fatalf("bad pool/sample sizes: %+v", first.ArtistPools)
	}
	for _, track := range first.Tracks {
		if len(track.GenreTags) > 0 || track.Ref.Artist == "Artist 000" || strings.HasSuffix(track.Ref.ID, "-0") {
			t.Fatal("artist tags, exclusion or recording identity leaked into sample")
		}
	}
	before := calls
	again := run(42)
	if calls != before || !reflect.DeepEqual(first, again) {
		t.Fatal("same seed did not replay from cached provider responses")
	}
	different := run(99)
	if reflect.DeepEqual(first.ArtistPools[0].SampledArtists, different.ArtistPools[0].SampledArtists) {
		t.Fatal("different seeds always picked the same artists")
	}
	if reflect.DeepEqual(first.ArtistPools[0].SampledArtists, []string{"0", "1", "2"}) {
		t.Fatal("selected the provider's first artists")
	}
}

func TestSmallGenrePoolRetainsProviderExhaustion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":1,"artists":[{"id":"a","name":"Only artist"}]}`))
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, time.Nanosecond)
	pool := c.genreArtists(context.Background(), "rare")
	if len(pool.Artists) != 1 || !pool.Complete || pool.Available != 1 {
		t.Fatal("invented missing artists or lost exhaustion")
	}
}
