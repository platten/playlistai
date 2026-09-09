package musicbrainz

import (
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/metadata"
)

func TestGenreDiscoveryWindowScalesButRemainsBounded(t *testing.T) {
	for _, tc := range []struct{ count, want int }{{0, 4}, {2, 4}, {10, 10}, {20, 20}, {100, 20}} {
		s := candidateStream{intent: core.MusicIntent{Controls: core.IntentControls{TotalTrackCount: tc.count}}}
		if got := s.discoveryWindowSize(); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestLocalGenreCandidatesRotateCatalogArtists(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a1", Display: "音楽家 - One"}, fakes.CatalogTrack{ID: "a2", Display: " 音楽家 - Two"}, fakes.CatalogTrack{ID: "a3", Display: "音楽家 - Three"},
		fakes.CatalogTrack{ID: "b", Display: "Other - Four"}, fakes.CatalogTrack{ID: "c", Display: "Third - Five"})
	input := []metadata.Match{{TrackID: "a1", ArtistID: 1, Source: "release/1"}, {TrackID: "a2", ArtistID: 2, Source: "release/2"}, {TrackID: "a3", ArtistID: 1, Source: "release/3"}, {TrackID: "b", ArtistID: 3, Source: "release/4"}, {TrackID: "c", ArtistID: 4, Source: "release/5"}}
	before := append([]metadata.Match(nil), input...)
	got := rotateLocalArtists(cat, input)
	want := []metadata.Match{input[0], input[3], input[4], input[1], input[2]}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(input, before) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(got, rotateLocalArtists(cat, input)) {
		t.Fatal("non-deterministic rotation")
	}
}
