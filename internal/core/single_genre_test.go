package core

import (
	"reflect"
	"testing"
)

func TestSingleGenreNormalizationPreservesMixedAndJourneyRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		genres   []IntentPreference
		criteria []MusicalCriterion
		want     bool
	}{
		{"single", []IntentPreference{{Value: "electronic"}}, nil, true},
		{"aliases", []IntentPreference{{Value: "electronic"}, {Value: "electronica"}}, nil, true},
		{"negative", []IntentPreference{{Value: "rock", Influence: InfluenceNegative}}, nil, false},
		{"mixed", []IntentPreference{{Value: "rock"}, {Value: "electronic"}}, nil, false},
		{"journey", []IntentPreference{{Value: "electronic"}}, []MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "journey_start"}}, false},
		{"artist journey global genre", nil, []MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}, true},
		{"mood only", nil, []MusicalCriterion{{Kind: "mood", Value: "relaxing", Scope: "playlist"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := MusicIntent{Version: CurrentIntentVersion, Preferences: SemanticPreferences{Genres: tc.genres}, EssentialCriteria: tc.criteria}
			normalized := original.Normalized()
			if _, ok := SinglePlaylistGenre(normalized); ok != tc.want {
				t.Fatalf("single genre=%v: %+v", ok, normalized)
			}
			if tc.want && len(normalized.EssentialCriteria) != 1 {
				t.Fatal("genre must survive as one essential criterion", normalized.EssentialCriteria)
			}
			if !reflect.DeepEqual(normalized, normalized.Normalized()) {
				t.Fatal("normalization is not idempotent")
			}
		})
	}
}
