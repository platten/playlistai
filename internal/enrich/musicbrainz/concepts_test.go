package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestDiscoveryIncludesSoftStylesAndApprovedAliases(t *testing.T) {
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{
		Styles: []core.IntentPreference{{Value: "hip-hop", Strength: "preferred", Influence: core.InfluencePositive}, {Value: "metal", Influence: core.InfluenceNegative}},
		Genres: []core.IntentPreference{{Value: "ambient electronic", Influence: core.InfluencePositive}},
	}}
	got := discoveryGenres(intent)
	want := []string{"ambient electronic", "hip hop", "hip-hop", "hiphop"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discovery queries = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"electronic", "metal"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("retrieval expanded parent or negative preference %q", forbidden)
		}
	}
}

func TestResolveMusicStyleOnlyUsesExactAliasWithoutInventingFit(t *testing.T) {
	var queried []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/recording":
			query := r.URL.Query().Get("query")
			queried = append(queried, query)
			if query == `tag:"hip-hop"` {
				_, _ = fmt.Fprint(w, `{"recordings":[{"id":"r1","title":"One","artist-credit":[{"name":"Artist","artist":{"id":"a1"}}],"tags":[{"name":"hip-hop","count":3}]}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"recordings":[]}`)
			}
		case "/ws/2/artist":
			_, _ = fmt.Fprint(w, `{"count":0,"artists":[]}`)
		case "/genres":
			_, _ = fmt.Fprint(w, `<html></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	intent := core.MusicIntent{Seed: "42", Count: 10, Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "hiphop", Influence: core.InfluencePositive, Strength: "preferred"}, {Value: "metal", Influence: core.InfluenceNegative}}}}
	got, err := client.ResolveMusic(context.Background(), intent, cat, cat, nil)
	if err != nil || got.Knowledge == nil || len(got.Knowledge.Candidates) != 1 {
		t.Fatalf("preferred style/alias failed to retrieve recording: %+v %v", got.Knowledge, err)
	}
	if !slices.Contains(queried, `tag:"hip-hop"`) || slices.Contains(queried, `tag:"metal"`) {
		t.Fatalf("wrong style retrieval queries: %q", queried)
	}
	if len(got.EssentialCriteria) != 0 || got.Preferences.Styles[0].Strength != "preferred" {
		t.Fatalf("retrieval promoted soft style to a requirement: %+v", got)
	}
	requests := len(queried)
	_, err = client.ResolveMusic(context.Background(), got, cat, cat, nil)
	if err != nil || len(queried) != requests {
		t.Fatalf("saved evidence replay performed discovery: %v", err)
	}
}

func TestCandidateAliasLookupFailureKeepsSuccessfulArtistPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/2/artist" {
			if r.URL.Query().Get("query") == `tag:"hip-hop"` {
				_, _ = fmt.Fprint(w, `{"count":1,"artists":[{"id":"a1","name":"Artist"}]}`)
			} else {
				_, _ = fmt.Fprint(w, `invalid provider response`)
			}
			return
		}
		_, _ = fmt.Fprint(w, `{"recordings":[{"id":"r1","title":"One","artist-credit":[{"name":"Artist","artist":{"id":"a1"}}]}]}`)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
	stream := client.OpenCandidates(core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "hiphop", Strength: "preferred"}}}}, cat, cat)
	got, err := stream.Next(context.Background())
	if err != nil || got.ID != "one" {
		t.Fatalf("alias failure discarded usable pool: %+v %v", got, err)
	}
	if len(stream.Snapshot().Notices) == 0 {
		t.Fatal("partial provider coverage was not recorded")
	}
}

func TestDiscoveryAliasInvarianceUnknownsAndJourneyStages(t *testing.T) {
	var first []string
	for _, spelling := range []string{"hip-hop", "HIP HOP", "hiphop"} {
		intent := core.MusicIntent{Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: spelling}}}, EssentialCriteria: []core.MusicalCriterion{
			{Kind: "style", Value: "electronica", Scope: "journey_start"},
			{Kind: "genre", Value: "未知のジャンル", Scope: "journey_end"},
		}}
		got := discoveryGenres(intent)
		if first == nil {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			t.Fatalf("spelling changes retrieval order/pool: %q versus %q", first, got)
		}
		if !slices.Contains(got, "未知のジャンル") || !slices.Contains(got, "electronica") || slices.Contains(got, "electronic") {
			t.Fatalf("unknown or stage meaning was lost: %q", got)
		}
	}
}
