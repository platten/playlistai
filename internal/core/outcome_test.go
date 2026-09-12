package core

import (
	"fmt"
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

func TestDurationOutcomeReconciliationRequiresEvidenceAndExplicitCount(t *testing.T) {
	intent := MusicIntent{DurationSeconds: 4500, Translation: &IntentTranslation{}, Controls: IntentControls{TotalTrackCount: 20}}.Normalized()
	assessment := &PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: 4500000, State: EvidenceMatch}
	for i := 0; i < 18; i++ {
		assessment.Evidence = append(assessment.Evidence, TrackDurationEvidence{TrackID: fmt.Sprint(i), RecordingDuration: RecordingDuration{Milliseconds: 250000, Source: "verified-provider", RecordingID: fmt.Sprint(i)}})
	}
	fulfilled := GenerationOutcome{State: OutcomeFulfilled}
	if got := ReconcileOutcome(fulfilled, intent, 18, assessment); got.State != OutcomeFulfilled {
		t.Fatalf("verified duration-only playlist demoted by default count: %+v", got)
	}
	intent.TrackCountExplicit = true
	if got := ReconcileOutcome(fulfilled, intent, 18, assessment); got.State != OutcomePartial || got.Reasons[0].Code != "requested_count_not_reached" {
		t.Fatalf("explicit count was relaxed: %+v", got)
	}
	intent.TrackCountExplicit = false
	for _, tc := range []struct {
		name     string
		verdict  GenerationOutcome
		duration *PlaylistDurationAssessment
	}{
		{"no verdict", GenerationOutcome{}, assessment},
		{"no evidence", fulfilled, nil},
		{"different target", fulfilled, &PlaylistDurationAssessment{TargetSeconds: 4400, ToleranceSeconds: 60, KnownMilliseconds: assessment.KnownMilliseconds, Evidence: assessment.Evidence, State: EvidenceMatch}},
		{"different tolerance", fulfilled, &PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 120, KnownMilliseconds: assessment.KnownMilliseconds, Evidence: assessment.Evidence, State: EvidenceMatch}},
		{"different total", fulfilled, &PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: 4500001, Evidence: assessment.Evidence, State: EvidenceMatch}},
		{"missing track evidence", fulfilled, &PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: assessment.KnownMilliseconds, Evidence: assessment.Evidence[:17], State: EvidenceMatch}},
		{"unknown track", fulfilled, &PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: assessment.KnownMilliseconds, Evidence: assessment.Evidence, UnknownTrackIDs: []string{"unknown"}, State: EvidenceMatch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReconcileOutcome(tc.verdict, intent, 18, tc.duration)
			if got.State != OutcomePartial {
				t.Fatalf("unverified duration became fulfilled: %+v", got)
			}
			if again := ReconcileOutcome(got, intent, 18, tc.duration); !reflect.DeepEqual(again, got) {
				t.Fatal("reconciliation duplicated duration reasons")
			}
		})
	}
	for _, state := range []GenerationOutcomeState{OutcomePartial, OutcomeUnsupported, OutcomeNeedsClarification} {
		before := GenerationOutcome{State: state, Reasons: []OutcomeReason{{Code: "duration_target_unmet"}}}
		if got := ReconcileOutcome(before, intent, 18, assessment); !reflect.DeepEqual(got, before) {
			t.Fatal("duration arithmetic erased an inability-to-fulfill verdict")
		}
	}
}
