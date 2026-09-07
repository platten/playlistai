package core

import "testing"

func TestUncertainStyleIsNotEvidenceOfAbsence(t *testing.T) {
	for _, confidence := range []float64{0, .5, .59} {
		feature := TrackFeatures{FacetCoverage: []string{"styles"}, Styles: []FeatureValue{{Value: "rock & roll", Missingness: FeatureKnown, Confidence: confidence, Provenance: []FeatureProvenance{{Source: "review"}}}}}
		if got := StyleEvidence(feature, "rock"); got != EvidenceUnknown {
			t.Errorf("confidence %.2f became absence: %s", confidence, got)
		}
		if SemanticConstraintSatisfied(feature, HardConstraint{Kind: "exclude_style", Value: "rock"}) {
			t.Fatal("uncertain rock passed no-rock constraint")
		}
	}
}

func TestJourneySequenceEvidenceRespectsDirectionAndDistinctStages(t *testing.T) {
	m, u := EvidenceMatch, EvidenceUnknown
	for _, tc := range []struct {
		name         string
		states       [][]EvidenceState
		stages, want int
	}{
		{"forward", [][]EvidenceState{{m, u}, {u, m}}, 2, 0},
		{"reverse", [][]EvidenceState{{u, m}, {m, u}}, 2, 2},
		{"hybrid bridge", [][]EvidenceState{{m, u}, {m, m}, {u, m}}, 2, 0},
		{"hybrid alone", [][]EvidenceState{{m, m}}, 2, 1},
		{"missing end", [][]EvidenceState{{m, u}, {m, u}}, 2, 1},
		{"missing via", [][]EvidenceState{{m, u, u}, {u, u, m}}, 3, 1},
		{"via", [][]EvidenceState{{m, u, u}, {u, m, u}, {u, u, m}}, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := JourneySequenceViolations(tc.states, tc.stages); got != tc.want {
				t.Fatalf("violations=%d, want %d", got, tc.want)
			}
		})
	}
}
