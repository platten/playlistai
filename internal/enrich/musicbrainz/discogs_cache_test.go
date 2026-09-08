package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestDiscogsConsolidatesSearchesAndPersistsSharedReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	cfg := Config{UserAgent: "fixture", CachePath: path}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		calls.Add(1)
		if r.URL.Path == "/database/search" {
			return jsonResponse(r, 200, `{"pagination":{"items":2},"results":[{"type":"release","id":7},{"type":"release","id":7}]}`)
		}
		return jsonResponse(r, 200, `{"id":7,"title":"Album","artists":[],"tracklist":[]}`)
	}
	c.discogs = discogsFixture(t, handler).discogs
	for _, query := range []url.Values{{"genre": {"Ambient"}}, {"artist": {"Artist"}}} {
		page, err := c.discogsSearch(context.Background(), query)
		if err != nil || len(page.Results) != 1 || page.Pagination.Items != 2 || page.FetchedResults != 2 {
			t.Fatalf("consolidation: %+v, %v", page, err)
		}
		if query.Has("type") || query.Has("per_page") {
			t.Fatal("caller query mutated")
		}
		if _, err = c.discogsRelease(context.Background(), page.Results[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("two searches should share one release fetch, got %d", calls.Load())
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.discogs = discogsFixture(t, func(r *http.Request) *http.Response {
		t.Error("fresh persisted result fetched again")
		return handler(r)
	}).discogs
	if _, err = c.discogsSearch(context.Background(), url.Values{"genre": {"Ambient"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.discogsRelease(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("restart lost fresh cache")
	}
}

func TestDiscogsConcurrentQueriesShareFetchAndDoNotMutateInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			calls.Add(1)
			return jsonResponse(r, 200, `{"results":[{"type":"release","id":7},{"type":"release","id":7}]}`)
		})
		query := url.Values{"genre": {"Ambient"}}
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				page, err := c.discogsSearch(context.Background(), query)
				if err != nil || len(page.Results) != 1 {
					t.Errorf("shared search: %+v %v", page, err)
				}
				if len(page.Results) > 0 {
					page.Results[0].ID = 999
				} // request-owned, not shared mutable cache state
			})
		}
		wg.Wait()
		page, err := c.discogsSearch(context.Background(), query)
		if err != nil || page.Results[0].ID != 7 || calls.Load() != 1 {
			t.Fatal("cache fetch or returned data was not isolated")
		}
		if query.Has("type") {
			t.Fatal("shared query mutated")
		}
	})
}

func TestDiscogsWrongReleaseCannotPoisonCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			calls++
			id := 7
			if calls == 1 {
				id = 8
			}
			return jsonResponse(r, 200, fmt.Sprintf(`{"id":%d,"tracklist":[]}`, id))
		})
		if _, err := c.discogsRelease(context.Background(), 7); err == nil {
			t.Fatal("wrong release accepted")
		}
		if len(c.memory) != 0 {
			t.Fatal("wrong release cached")
		}
		if _, err := c.discogsRelease(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatal("invalid cache prevented retry")
		}
		// Invalid entries written by older versions must also be rejected.
		key := metadataKey(c.discogs.base, "/releases/7", "discogs-v1:")
		c.writeCache(context.Background(), key, []byte(`{"id":8,"tracklist":[]}`), 0)
		if _, err := c.discogsRelease(context.Background(), 7); err != nil || calls != 3 {
			t.Fatal("invalid legacy cache entry reused")
		}
	})
}

func TestConsolidatedDiscogsAlbumRetainsPageCompleteness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := discogsFixture(t, func(r *http.Request) *http.Response {
			if r.URL.Path == "/database/search" {
				return jsonResponse(r, 200, `{"pagination":{"items":2},"results":[{"id":7,"type":"release"},{"id":7,"type":"release"}]}`)
			}
			return jsonResponse(r, 200, `{"id":7,"title":"Album","artists":[{"name":"Artist"}],"tracklist":[{"type_":"track","title":"One"}]}`)
		})
		cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
		var snapshot core.KnowledgeSnapshot
		ref := core.IntentReference{Kind: core.ReferenceAlbum, Query: "Artist - Album"}
		got, ok := c.resolveDiscogsAlbum(context.Background(), ref, cat, cat, &snapshot)
		if !ok || got.Resolution == nil || got.Resolution.Status != core.ResolutionResolved || got.TrackID != "one" {
			t.Fatalf("duplicate removal made a complete search ambiguous: %+v", got)
		}
	})
}
