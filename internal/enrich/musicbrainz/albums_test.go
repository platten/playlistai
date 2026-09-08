package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/resolution"
)

func TestAlbumFallbackCorroboratesArtistAndRecordingVersion(t *testing.T) {
	client, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/release-group":
			http.Error(w, "busy", http.StatusServiceUnavailable)
		case "/search/album":
			if r.URL.Query().Get("q") != "Fixture Album Canonical Artist" {
				t.Error("qualified album query lost")
			}
			fmt.Fprint(w, `{"data":[{"id":1,"title":"Fixture Album","artist":{"name":"Wrong First Result"}},{"id":7,"title":"Fixture Album","artist":{"name":"Canonical Artist"}}]}`)
		case "/album/7/tracks":
			fmt.Fprint(w, `{"data":[{"id":1,"title":"Missing song","artist":{"name":"Canonical Artist"}},{"id":2,"title":"Second song","artist":{"name":"Canonical Artist"}},{"id":3,"title":"Versioned song","artist":{"name":"Canonical Artist"}}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	})
	cat := seedTestCatalog()
	snapshot := core.KnowledgeSnapshot{}
	ref := client.resolveAlbum(context.Background(), core.IntentReference{Kind: core.ReferenceAlbum, Query: "Canonical Artist - Fixture Album"}, cat, cat, &snapshot)
	if ref.Resolution == nil || ref.Resolution.Status != core.ResolutionResolved || ref.TrackID != "match" || len(ref.Resolution.Selected.Representatives) != 1 {
		t.Fatalf("album=%+v", ref)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].ID != "match" || len(snapshot.Tracks) != 0 {
		t.Fatal("wrong version or invented recording genre evidence")
	}
	if len(snapshot.Notices) == 0 || !strings.Contains(snapshot.Notices[0], "unavailable") {
		t.Fatal("outage hidden as catalog absence")
	}
	before := calls.Load()
	ref = client.resolveDeezerAlbum(context.Background(), core.IntentReference{Kind: core.ReferenceAlbum, Query: "Fixture Album by Canonical Artist"}, cat, cat, &core.KnowledgeSnapshot{})
	if calls.Load() != before || ref.TrackID != "match" {
		t.Fatal("equivalent album query did not reuse metadata cache")
	}
}

func TestAlbumFallbackRetainsAmbiguousIdentitiesAcrossResolution(t *testing.T) {
	client, _ := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/album" {
			t.Fatalf("ambiguous album fetched recordings: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":7,"title":"Fixture Album","artist":{"name":"Canonical Artist"}},{"id":8,"title":"Fixture Album","artist":{"name":"Canonical Artist"}}]}`)
	})
	cat := seedTestCatalog()
	ref := client.resolveDeezerAlbum(context.Background(), core.IntentReference{Kind: core.ReferenceAlbum, Query: "Fixture Album by Canonical Artist"}, cat, cat, &core.KnowledgeSnapshot{})
	intent, issues := resolution.Apply(cat, core.MusicIntent{Version: core.CurrentIntentVersion, References: []core.IntentReference{ref}})
	if len(issues) != 1 || issues[0].Status != core.ResolutionAmbiguous || len(intent.References[0].Resolution.Alternatives) != 2 {
		t.Fatalf("ambiguity lost: %+v", issues)
	}
}

func TestIncompleteAlbumSearchCannotEstablishUniqueIdentity(t *testing.T) {
	for _, provider := range []string{"musicbrainz", "deezer"} {
		t.Run(provider, func(t *testing.T) {
			client, _ := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/ws/2/release-group":
					if provider == "deezer" {
						http.Error(w, "busy", http.StatusServiceUnavailable)
						return
					}
					fmt.Fprint(w, `{"count":6,"release-groups":[{"id":"fixture","title":"Fixture Album","artist-credit":[{"name":"Canonical Artist"}]}]}`)
				case "/search/album":
					fmt.Fprint(w, `{"total":26,"data":[{"id":7,"title":"Fixture Album","artist":{"name":"Canonical Artist"}}]}`)
				default:
					t.Errorf("selected album before search completed: %s", r.URL.Path)
				}
			})
			cat := seedTestCatalog()
			ref := client.resolveAlbum(context.Background(), core.IntentReference{Kind: core.ReferenceAlbum, Query: "Fixture Album by Canonical Artist"}, cat, cat, &core.KnowledgeSnapshot{})
			if ref.Resolution == nil || ref.Resolution.Status != core.ResolutionAmbiguous || ref.Resolution.Selected != nil || ref.TrackID != "" {
				t.Fatalf("partial search picked an album: %+v", ref)
			}
		})
	}
}
