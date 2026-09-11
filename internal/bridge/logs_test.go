package bridge

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
)

func TestConcurrentDebugPreferencesKeepPersistenceAndCaptureConsistent(t *testing.T) {
	c := newTestContainer(t)
	store := &logging.Store{}
	api := NewWithLogs(c, nil, store)
	for range 20 {
		var writers sync.WaitGroup
		for writer := range 8 {
			writers.Add(1)
			go func() {
				defer writers.Done()
				if err := api.SetDebugLogging(writer%2 == 0); err != nil {
					t.Error(err)
				}
			}()
		}
		writers.Wait()
		if saved, active := c.DebugLogging(), api.GetDebugLogging(); saved != active {
			t.Fatalf("saved debug preference %v differs from active capture %v", saved, active)
		}
	}
	if err := api.SetDebugLogging(false); err != nil {
		t.Fatal(err)
	}
	if c.DebugLogging() || store.DebugEnabled() {
		t.Fatal("the final opt-out was not applied everywhere")
	}
}

func TestDebugPreferenceEnablesOrdinaryGoLogsOnlyInViewer(t *testing.T) {
	c := newTestContainer(t)
	store := &logging.Store{}
	var output bytes.Buffer
	log := slog.New(logging.NewHandler(slog.NewTextHandler(&output, nil), store))
	api := NewWithLogs(c, log, store)
	log.Debug("before opt-in")
	if err := api.SetDebugLogging(true); err != nil {
		t.Fatal(err)
	}
	log.Debug("runtime detail", "attempt", 1)
	records := api.GetLogs(0)
	if len(records) != 1 || records[0].Level != "DEBUG" || !strings.Contains(records[0].Text, "attempt=1") {
		t.Fatalf("Go DEBUG entry missing from log viewer: %+v", records)
	}
	if output.Len() != 0 {
		t.Fatalf("opt-in DEBUG leaked to process output: %s", output.String())
	}
	if err := api.SetDebugLogging(false); err != nil {
		t.Fatal(err)
	}
	log.Debug("after opt-out")
	if len(api.GetLogs(0)) != 0 || api.GetDebugLogging() {
		t.Fatal("opt-out did not stop and clear Go DEBUG entries")
	}
}

func TestDebugLoggingPreferenceAndRecommendationDetails(t *testing.T) {
	c := newTestContainer(t)
	store := &logging.Store{}
	api := NewWithLogs(c, slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	if api.GetDebugLogging() {
		t.Fatal("debug diagnostics enabled by default")
	}
	if err := api.SetDebugLogging(true); err != nil {
		t.Fatal(err)
	}
	if !config.LoadPrefs(c.Config().DataDir).DebugLogging || !api.GetDebugLogging() {
		t.Fatal("debug preference was not persisted and applied")
	}

	ctx := api.diagnosticContext(context.Background())
	score := 0.8
	bpm := 123.5
	result := PlaylistResult{
		GenerationID: "generation-one",
		Outcome:      core.GenerationOutcome{State: core.OutcomeFulfilled},
		Tracks: []PlaylistTrack{{
			ID: "track-one", Artist: "Artist", Title: "Track", Kind: "ranked", Detail: "highest eligible score",
			Evidence: []core.ComponentEvidence{{Component: "acousticbrainz_intent", Score: score, Available: true}},
		}},
		Assessments: []core.TrackAssessment{{TrackID: "track-one", Comparisons: []core.IntentComparison{{AcousticState: "supporting", AcousticScore: &score}}}},
		Intent: core.MusicIntent{Knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: core.TrackRef{ID: "track-one"}, Acoustic: &core.AcousticCharacteristics{
			Source: "acousticbrainz", HighStatus: "available", Low: &core.AcousticMeasurements{BPM: &bpm},
			Predictions: map[string]core.AcousticPrediction{"danceability": {Value: "danceable", Score: 0.9, Classes: map[string]float64{"danceable": 0.9}}},
		}}}}},
		AudioEvidence: &core.AudioEvidenceSnapshot{Model: core.AudioModelIdentity{Model: "clap"}, Assessments: []core.AudioAssessment{{TrackID: "track-one", Eligible: true}}},
	}
	logRecommendationDiagnostics(ctx, result)
	joined := ""
	for _, entry := range api.GetLogs(0) {
		joined += entry.Text + "\n"
	}
	for _, event := range []string{"recommendation.result", "recommendation.pick", "analysis.acousticbrainz_comparison", "analysis.acousticbrainz_features", "analysis.clap", "analysis.clap_summary"} {
		if !strings.Contains(joined, `event="`+event+`"`) {
			t.Fatalf("missing %s diagnostic in %s", event, joined)
		}
	}
	if !strings.Contains(joined, `"bpm":123.5`) || !strings.Contains(joined, `"danceable":0.9`) {
		t.Fatalf("AcousticBrainz feature data missing from diagnostics: %s", joined)
	}
	if err := api.SetDebugLogging(false); err != nil {
		t.Fatal(err)
	}
	if len(api.GetLogs(0)) != 0 || config.LoadPrefs(c.Config().DataDir).DebugLogging {
		t.Fatal("disabling debug did not clear diagnostics and preference")
	}
}
