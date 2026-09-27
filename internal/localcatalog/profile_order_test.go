package localcatalog

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestEnhancedDiscoveryProfilesDoNotTruncateAlphabeticalTies(t *testing.T) {
	// Baseline case 2 kept precisely these first twelve names despite later
	// artists occurring in the same bounded electronic metadata page. All tags
	// here are retrieval leads; this test does not assert musical suitability.
	names := []string{"A Perfect Circle", "Aerosmith", "Air", "Alice Cooper", "All Saints", "Armin van Buuren;Ferry Corsten;Rank 1;Ruben de Ronde", "Bad Bunny", "Beamy", "Bee Gees", "Ben Böhmer", "Black Coffee", "Blackshore", "Boards of Canada", "Daft Punk", "Depeche Mode", "Massive Attack", "The Chemical Brothers", "Underworld", "Was (Not Was)", "浜崎あゆみ"}
	var tracks []librarypack.Track
	for i, name := range names {
		tracks = append(tracks, librarypack.Track{ID: fmt.Sprintf("recording-%02d", i), Artist: name, Title: "Song", RawTags: []byte(`{"GENRE":"electronic"}`)})
	}
	cat, _ := openTestCatalog(t, tracks, nil)
	defer cat.Close()
	intent := core.MusicIntent{Seed: "42", OriginalDescription: "Electronic music. Make a 10-song playlist.", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}}
	read := func(intent core.MusicIntent) []string {
		t.Helper()
		profiles, err := cat.DiscoveryProfiles(context.Background(), intent, 12)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range profiles {
			out = append(out, p.Artist)
		}
		return out
	}
	first := read(intent)
	if len(first) != 12 || reflect.DeepEqual(first, names[:12]) {
		t.Fatalf("alphabetical names consumed the profile budget: %v", first)
	}
	late := false
	for _, name := range first {
		late = late || name >= "M"
	}
	if !late {
		t.Fatalf("later artists still received no profile opportunity: %v", first)
	}
	if again := read(intent); !reflect.DeepEqual(first, again) {
		t.Fatalf("same intent and seed changed discovery order: %v / %v", first, again)
	}
	intent.Seed = "042"
	intent.OriginalDescription = "  ELECTRONIC MUSIC. Make a 10-song playlist.  "
	if again := read(intent); !reflect.DeepEqual(first, again) {
		t.Fatalf("equivalent seed or normalized description changed ties: %v / %v", first, again)
	}
	intent.Seed = "43"
	if again := read(intent); reflect.DeepEqual(first, again) {
		t.Fatal("changing the seed did not vary tied profile opportunities")
	}
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Underworld", Influence: core.InfluencePositive}}
	if got := read(intent); len(got) == 0 || got[0] != "Underworld" {
		t.Fatalf("tie ordering overrode the explicit artist: %v", got)
	}
	intent.References = nil
	intent.Controls.RecommendationMode = core.DeejAIOnly
	if got := read(intent); !reflect.DeepEqual(got, names[:12]) {
		t.Fatalf("another mode's existing discovery policy changed: %v", got)
	}
}
