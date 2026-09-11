package bridge

import (
	"context"
	"math"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestLegacyCompleteDoesNotVerifyMusicalCriteria(t *testing.T) {
	result := migrateLoadedResult(PlaylistResult{
		Intent: core.MusicIntent{Controls: core.IntentControls{TotalTrackCount: 1}, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "electronic"}}},
		Tracks: []PlaylistTrack{{ID: "track"}}, Status: GenerationStatus{State: "complete"},
	})
	if result.Outcome.State != core.OutcomePartial || result.Status.State != "partial" || len(result.Status.Reasons) != 1 || result.Status.Reasons[0].Code != "musical_verification_unavailable" {
		t.Fatalf("legacy count mistaken for musical fulfillment: %+v", result)
	}
}

func TestDomainOutcomeOverridesConflictingStoredStatus(t *testing.T) {
	result := migrateLoadedResult(PlaylistResult{Tracks: []PlaylistTrack{{ID: "track"}}, Outcome: core.GenerationOutcome{State: core.OutcomeUnsupported, Reasons: []core.OutcomeReason{{Code: "missing_evidence"}}}, Status: GenerationStatus{State: "fulfilled"}})
	if result.Status.State != "unsupported" || result.Status.Reasons[0].Code != "missing_evidence" {
		t.Fatal("two competing outcome authorities", result)
	}
}

func TestLegacyCompletePreservesUnsupportedRequirement(t *testing.T) {
	result := migrateLoadedResult(PlaylistResult{
		Intent: core.MusicIntent{
			Controls:    core.IntentControls{TotalTrackCount: 1},
			Unsupported: []core.UnsupportedRequirement{{Text: "120 BPM", Reason: "tempo cannot be verified"}},
		},
		Tracks: []PlaylistTrack{{ID: "track"}}, Status: GenerationStatus{State: "complete"},
	})
	if result.Outcome.State != core.OutcomePartial || result.Status.State != "partial" {
		t.Fatalf("unsupported legacy requirement presented as fulfilled: %+v", result)
	}
}

func TestUnencodableResultDoesNotWriteBrokenHistory(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion}.Normalized()
	a.saveGenerated(context.Background(), "name", "prompt", intent, BuildPlaylistRequest{Intent: intent}, PlaylistResult{Intent: intent, Tracks: []PlaylistTrack{{Evidence: []core.ComponentEvidence{{Score: math.NaN()}}}}})
	history, err := a.ListSavedPlaylists()
	if err != nil || len(history) != 0 {
		t.Fatalf("invalid history persisted: %+v %v", history, err)
	}
}
