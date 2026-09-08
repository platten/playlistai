package core

import "testing"

func TestWantsInstrumentalPreservesStructuredIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent MusicIntent
		want   bool
	}{
		{"hard exclusion", MusicIntent{HardConstraints: []HardConstraint{{Kind: "exclude_vocals"}}}, true},
		{"required instrumental", MusicIntent{HardConstraints: []HardConstraint{{Kind: "require_instrumental"}}}, true},
		{"vocal preference", MusicIntent{Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "No vocals", Influence: InfluencePositive}}}, true},
		{"negative singing", MusicIntent{Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "singing", Influence: InfluenceNegative}}}, true},
		{"instrumentation", MusicIntent{Preferences: SemanticPreferences{Instrumentation: []IntentPreference{{Value: "instrumental", Influence: InfluencePositive}}}}, true},
		{"exclude instrumental", MusicIntent{Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "instrumental", Influence: InfluenceNegative}}}, false},
		{"female vocals", MusicIntent{Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "female vocals", Influence: InfluencePositive}}}, false},
		{"reference title", MusicIntent{References: []IntentReference{{Kind: ReferenceTrack, Query: "Instrumental"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WantsInstrumental(tc.intent); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
