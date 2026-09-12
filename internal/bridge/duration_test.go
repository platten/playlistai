package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/history"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestDurationControlsAndAssessmentSurviveHistory(t *testing.T) {
	c := newLoadedContainer(t)
	api := New(c, nil)
	source := lexicon.Extract("One hour and fifteen minutes of relaxing electronic music for work, mostly instrumental.")
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, DurationSeconds: 4500, DurationToleranceSeconds: 60, Translation: &source, Count: 20}.Normalized()
	if got := applyOverrides(intent, ControlOverrides{}); got.HasExplicitTrackCount() {
		t.Fatal("unrelated controls fixed duration-only count")
	}
	n := 18
	intent = applyOverrides(intent, ControlOverrides{TotalTrackCount: &n}).Normalized()
	if !intent.HasExplicitTrackCount() || intent.Count != 18 {
		t.Fatal("explicit count override lost", intent)
	}
	result := PlaylistResult{Intent: intent, Duration: &core.PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: 4500000, State: core.EvidenceMatch, Evidence: []core.TrackDurationEvidence{{TrackID: "seed0001", RecordingDuration: core.RecordingDuration{Milliseconds: 4500000, Source: "fixture", RecordingID: "recording"}}}}}
	requestJSON, err := json.Marshal(BuildPlaylistRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	record, err := c.History.Save(context.Background(), history.Record{Name: "Duration", RequestJSON: requestJSON, ResultJSON: resultJSON})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := api.LoadSavedPlaylist(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result.Duration == nil || loaded.Result.Duration.KnownMilliseconds != 4500000 || loaded.Result.Duration.ToleranceSeconds != 60 || len(loaded.Result.Duration.Evidence) != 1 || !loaded.Request.Intent.HasExplicitTrackCount() {
		t.Fatalf("duration history lost: %+v", loaded)
	}
}

func TestDurationOnlyFulfillmentSurvivesResultAndHistoryBoundary(t *testing.T) {
	c := newLoadedContainer(t)
	api := New(c, nil)
	source := lexicon.Extract("One hour and fifteen minutes of music.")
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, DurationSeconds: 4500, DurationToleranceSeconds: 60, Count: 20, Translation: &source}.Normalized()
	assessment := &core.PlaylistDurationAssessment{TargetSeconds: 4500, ToleranceSeconds: 60, KnownMilliseconds: 4500000, State: core.EvidenceMatch}
	var tracks []PlaylistTrack
	for i := 0; i < 18; i++ {
		id := fmt.Sprintf("track-%d", i)
		tracks = append(tracks, PlaylistTrack{ID: id})
		assessment.Evidence = append(assessment.Evidence, core.TrackDurationEvidence{TrackID: id, RecordingDuration: core.RecordingDuration{Milliseconds: 250000, Source: "fixture", RecordingID: id}})
	}
	result := PlaylistResult{Intent: intent, Tracks: tracks, Duration: assessment, Outcome: core.GenerationOutcome{State: core.OutcomeFulfilled}}
	result.Outcome = core.ReconcileOutcome(result.Outcome, result.Intent, len(result.Tracks), result.Duration)
	if result.Outcome.State != core.OutcomeFulfilled {
		t.Fatal("bridge demoted valid duration-only result", result.Outcome)
	}
	requestJSON, err := json.Marshal(BuildPlaylistRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	record, err := c.History.Save(context.Background(), history.Record{Name: "75 minutes", RequestJSON: requestJSON, ResultJSON: resultJSON})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := api.LoadSavedPlaylist(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result.Outcome.State != core.OutcomeFulfilled || loaded.Result.Status.State != string(core.OutcomeFulfilled) {
		t.Fatal("history demoted fulfilled duration", loaded.Result.Outcome, loaded.Result.Status)
	}
	result.Duration = nil
	if got := migrateLoadedResult(result); got.Outcome.State == core.OutcomeFulfilled {
		t.Fatal("legacy duration without evidence reported verified")
	}
}
