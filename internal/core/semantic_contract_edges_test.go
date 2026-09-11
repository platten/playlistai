package core

import (
	"reflect"
	"testing"
)

func TestSemanticEvidenceContracts(t *testing.T) {
	known := func(value string) FeatureValue {
		return FeatureValue{Value: value, Missingness: FeatureKnown, Confidence: .9, Provenance: []FeatureProvenance{{Source: "review"}}}
	}
	for _, tc := range []struct {
		actual, want string
		state        EvidenceState
	}{{"mixed", "vocal", EvidenceMatch}, {"instrumental", "instrumental", EvidenceMatch}, {"vocal", "instrumental", EvidenceMismatch}, {"unclassified", "vocal", EvidenceUnknown}} {
		if got := VocalEvidence(TrackFeatures{VocalEvidence: known(tc.actual)}, tc.want); got != tc.state {
			t.Fatalf("vocal %s/%s: %s", tc.actual, tc.want, got)
		}
	}
	if VocalEvidence(TrackFeatures{}, "instrumental") != EvidenceUnknown {
		t.Fatal("missing vocals treated as instrumental")
	}
	features := TrackFeatures{Styles: []FeatureValue{known("techno")}, Moods: []FeatureValue{known("Calm")}, Instrumentation: []FeatureValue{known("piano")}, VocalEvidence: known("instrumental"), FacetCoverage: []string{"all"}}
	for _, criterion := range []MusicalCriterion{{Kind: "style", Value: "electronic"}, {Kind: "mood", Value: " calm "}, {Kind: "instrumentation", Value: "piano"}, {Kind: "vocal", Value: "instrumental"}} {
		if got := CriterionEvidence(features, criterion); got != EvidenceMatch {
			t.Fatalf("%v: %s", criterion, got)
		}
	}
	if CriterionEvidence(features, MusicalCriterion{Kind: "tempo"}) != EvidenceUnsupported {
		t.Fatal("unsupported criterion accepted")
	}
	if CriterionEvidence(features, MusicalCriterion{Kind: "mood", Value: "angry"}) != EvidenceMismatch {
		t.Fatal("complete mood absence not recognized")
	}
	for _, constraint := range []HardConstraint{{Kind: "exclude_style", Value: "rock"}, {Kind: "require_style", Value: "electronic"}, {Kind: "exclude_vocals"}, {Kind: "require_instrumental"}} {
		if !SemanticConstraintSatisfied(features, constraint) {
			t.Fatalf("supported evidence rejected: %v", constraint)
		}
	}
	if SemanticConstraintSatisfied(features, HardConstraint{Kind: "require_vocals"}) || SemanticConstraintSatisfied(features, HardConstraint{Kind: "unknown"}) {
		t.Fatal("unsupported constraint fulfilled")
	}
	if CanonicalStyle("rock n roll") != "rock & roll" || StyleMatches("techno", "electronic") {
		t.Fatal("genre direction or alias incorrect")
	}
	if !FacetComplete(TrackFeatures{FacetCoverage: []string{"styles_and_tags"}}, "tags") {
		t.Fatal("combined coverage missing")
	}
	if JourneySequenceViolations(nil, 3) != 3 || JourneySequenceViolations(nil, 0) != 0 {
		t.Fatal("empty journey missing-stage accounting")
	}
	criteria := []MusicalCriterion{{Scope: "journey_end", Value: "end"}, {Scope: "playlist"}, {Scope: "journey_via", Value: "via1"}, {Scope: "journey_start", Value: "start"}, {Scope: "journey_via", Value: "via2"}}
	got := JourneyCriteria(criteria)
	if len(got) != 4 || got[0].Value != "start" || got[1].Value != "via1" || got[2].Value != "via2" || got[3].Value != "end" || criteria[0].Value != "end" {
		t.Fatalf("journey ordering mutated input or lost stages: %v", got)
	}
}

func TestNormalizationPreservesMeaningAndRemovesEmptyDuplicates(t *testing.T) {
	ref := IntentReference{Query: " Artist ", Resolution: &ReferenceResolution{Status: ResolutionResolved, Selected: &ResolutionCandidate{Kind: ReferenceArtist, Representatives: []WeightedTrack{{TrackID: "representative", Weight: 1}}}}}
	m := MusicIntent{Version: CurrentIntentVersion, References: []IntentReference{ref, {Query: "artist"}, {}}, RequiredTracks: []IntentReference{{TrackID: "required"}},
		InferredAnchors:   []InferredAnchor{{}, {Reference: IntentReference{Query: "hint"}}, {Reference: IntentReference{Query: "HINT"}}},
		EssentialCriteria: []MusicalCriterion{{}, {Kind: " STYLE ", Value: " jazz "}, {Kind: "style", Value: "Jazz"}},
		Preferences:       SemanticPreferences{Genres: []IntentPreference{{Value: " "}, {Value: " jazz "}}, VocalPreference: &IntentPreference{Value: " instrumental "}},
		Unsupported:       []UnsupportedRequirement{{}, {Text: " Original ", Reason: " reason "}, {Text: "original"}},
		HardConstraints:   []HardConstraint{{Kind: "require_tempo", Value: "120", Evidence: []SourceEvidence{{Text: "Original"}}}, {Kind: "require_key", Value: "C"}, {Kind: "exclude_reference_artists", Value: "true"}, {Kind: "no_back_to_back_artist", Value: "true"}},
	}
	normalized := m.Normalized()
	if len(normalized.References) != 1 || len(normalized.InferredAnchors) != 1 || len(normalized.EssentialCriteria) != 1 {
		t.Fatalf("duplicates or blanks retained: %+v", normalized)
	}
	if len(normalized.Preferences.Genres) != 1 || normalized.Preferences.Genres[0].Influence != InfluencePositive || normalized.Preferences.VocalPreference.Value != "instrumental" {
		t.Fatal("preference normalization lost meaning")
	}
	if len(normalized.Unsupported) != 2 || normalized.Unsupported[0].Reason != "reason" {
		t.Fatalf("unsupported requirements: %v", normalized.Unsupported)
	}
	if !reflect.DeepEqual(normalized.Required.TrackIDs, []string{"required"}) || !reflect.DeepEqual(normalized.Seeds.TrackIDs, []string{"representative"}) || !normalized.Constraints.ExcludeSeedArtists || !normalized.Constraints.NoRepeatArtistBackToBack {
		t.Fatalf("legacy adapter lost identities or constraints: %+v", normalized)
	}
	emptyVocal := MusicIntent{Version: CurrentIntentVersion, Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: " "}}}.Normalized()
	if emptyVocal.Preferences.VocalPreference != nil {
		t.Fatal("blank vocal preference retained")
	}
	if !WantsInstrumental(MusicIntent{Preferences: SemanticPreferences{Instrumentation: []IntentPreference{{Value: "instrumental"}}}}) {
		t.Fatal("instrumentation intent ignored")
	}
	if (TrackRef{}).SpotifyURL() != "" {
		t.Fatal("empty ID generated URL")
	}
}
