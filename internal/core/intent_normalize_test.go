package core

import (
	"math"
	"reflect"
	"testing"
)

func TestNormalizationDoesNotMutateVocalPreference(t *testing.T) {
	preference := &IntentPreference{Value: " instrumental "}
	intent := MusicIntent{Version: CurrentIntentVersion, Preferences: SemanticPreferences{VocalPreference: preference}}
	got := intent.Normalized()
	if preference.Value != " instrumental " || preference.Influence != "" {
		t.Fatal("normalization mutated the source/cached intent")
	}
	if got.Preferences.VocalPreference == preference || got.Preferences.VocalPreference.Value != "instrumental" || got.Preferences.VocalPreference.Influence != InfluencePositive {
		t.Fatal("normalized preference lacks independent canonical state")
	}
	if twice := got.Normalized(); !reflect.DeepEqual(twice, got) {
		t.Fatal("normalization is not idempotent")
	}
}

func TestResolutionRejectsNonFiniteConfidenceAndWeight(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, field := range []string{"confidence", "weight"} {
			candidate := ResolutionCandidate{Kind: ReferenceArtist, Confidence: 1, Representatives: []WeightedTrack{{TrackID: "id", Weight: 1}}}
			if field == "confidence" {
				candidate.Confidence = value
			} else {
				candidate.Representatives[0].Weight = value
			}
			resolution := ReferenceResolution{Status: ResolutionResolved, Selected: &candidate}
			if err := validateResolution(ReferenceArtist, resolution); err == nil {
				t.Fatalf("nonfinite %s accepted", field)
			}
		}
	}
}
