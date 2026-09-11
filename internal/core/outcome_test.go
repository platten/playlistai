package core

import (
	"reflect"
	"testing"
)

func TestReconcileOutcomeDoesNotInventMusicalFulfillment(t *testing.T) {
	for _, tc := range []struct {
		name              string
		state             GenerationOutcomeState
		count             int
		essential, strict bool
		want              GenerationOutcomeState
	}{
		{"reference complete", "", 10, false, false, OutcomeFulfilled},
		{"reference short", "", 5, false, false, OutcomePartial},
		{"empty", "", 0, false, false, OutcomePartial},
		{"unknown genre", "", 10, true, false, OutcomePartial},
		{"unknown strict", "", 10, false, true, OutcomePartial},
		{"unknown no result", "", 0, true, false, OutcomeUnsupported},
		{"verified genre", OutcomeFulfilled, 10, true, false, OutcomeFulfilled},
		{"inconsistent count", OutcomeFulfilled, 5, false, false, OutcomePartial},
		{"partial remains partial", OutcomePartial, 10, true, false, OutcomePartial},
		{"unsupported remains unsupported", OutcomeUnsupported, 10, false, true, OutcomeUnsupported},
		{"conflict remains conflict", OutcomeNeedsClarification, 10, true, true, OutcomeNeedsClarification},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := MusicIntent{Count: 10}
			if tc.essential {
				intent.EssentialCriteria = []MusicalCriterion{{Kind: "genre", Value: "electronic"}}
			}
			if tc.strict {
				intent.HardConstraints = []HardConstraint{{Kind: "require_style", Value: "electronic"}}
			}
			got := ReconcileOutcome(GenerationOutcome{State: tc.state}, intent, tc.count)
			if got.State != tc.want {
				t.Fatalf("got %+v", got)
			}
			if again := ReconcileOutcome(got, intent, tc.count); !reflect.DeepEqual(again, got) {
				t.Fatal("verdict reconciliation duplicated reasons")
			}
		})
	}
}
