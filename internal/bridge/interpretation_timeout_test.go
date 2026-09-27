package bridge

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
)

const timeoutJourneyPrompt = "Make a 10-song journey from ambient electronic through downtempo to melodic house, gradually increasing the energy."

func TestInterpretationWorkTimeoutPreservesSourceRequest(t *testing.T) {
	for _, override := range []int{0, 14} {
		t.Run("override_"+strconv.Itoa(override), func(t *testing.T) {
			c := newLoadedContainer(t)
			if err := c.SetRecommendationMode(core.Automatic); err != nil {
				t.Fatal(err)
			}
			a := New(c, nil)
			runtime := a.runtime()
			runtime.AutomaticReco = failAfterParsingEngine{}
			a.runtime = func() app.RuntimeSnapshot { return runtime }
			source := lexicon.Extract(timeoutJourneyPrompt)
			before := source.Clone()
			input := ports.IntentInput{Prompt: timeoutJourneyPrompt, TrackCount: override, SourceFacts: &source, PreparedMusicSnapshot: "pinned-graph"}
			// The work deadline has passed, but the parent still has four seconds
			// of its finalization reserve. No model or provider is involved.
			ctx, cancel := ports.WithAutomaticGenerationBudgetSince(context.Background(), time.Now().Add(-ports.AutomaticGenerationLimit+4*time.Second))
			defer cancel()
			result, err := a.generateFromPrompt(ctx, input, nil)
			if err != nil || ctx.Err() != nil {
				t.Fatalf("work timeout became operation failure: err=%v parent=%v", err, ctx.Err())
			}
			wantCount := 10
			if override > 0 {
				wantCount = override
			}
			intent := result.Request.Intent
			if intent.Count != wantCount || intent.Controls.TotalTrackCount != wantCount || intent.Mode != core.ModeJourney || intent.Controls.RecommendationMode != core.Automatic || intent.PreparedMusicSnapshot != "pinned-graph" {
				t.Fatalf("source controls or graph pin lost: %+v", intent)
			}
			stages := map[string]string{}
			for _, criterion := range core.JourneyCriteria(intent.EssentialCriteria) {
				stages[criterion.Scope] = criterion.Value
			}
			wantStages := map[string]string{"journey_start": "ambient electronic", "journey_via": "downtempo", "journey_end": "melodic house"}
			if !reflect.DeepEqual(stages, wantStages) || len(intent.Journey.EnergyTrajectory) != 0 {
				t.Fatalf("source journey lost or an unparsed energy trajectory invented: %v / %v", stages, intent.Journey.EnergyTrajectory)
			}
			if result.Status.Parser.Backend != "rules" || result.Status.Parser.RequestedBackend != "rules" || !result.Status.Parser.FallbackUsed || result.Status.Parser.FallbackReason != "timeout" || len(result.Status.Timings) != 1 {
				t.Fatalf("timeout fallback not disclosed: %+v", result.Status)
			}
			if result.Playlist.Outcome.State != core.OutcomePartial || len(result.Playlist.Tracks) != 0 || result.Playlist.Search != nil || !reflect.DeepEqual(result.Playlist.Status, result.Status) {
				t.Fatal("timeout claimed generation or lost its status")
			}
			if len(a.intentCache.entries) != 0 || !reflect.DeepEqual(source, before) {
				t.Fatal("timeout cached or mutated source interpretation")
			}
			history, err := c.History.ListSummaries(context.Background(), 10)
			if err != nil || len(history) != 0 {
				t.Fatalf("timeout wrote history: %v / %v", history, err)
			}
		})
	}
}

func TestInterpretationTimeoutRetainsPreparedSourceAndAttemptedBackend(t *testing.T) {
	source := lexicon.Extract(timeoutJourneyPrompt)
	source.Recognition = core.RecognitionStatus{ReferenceLookup: "available", ReferenceSnapshot: "frozen-identities"}
	prepared := ports.IntentInput{Prompt: timeoutJourneyPrompt, SourceFacts: &source, PreparedMusicSnapshot: "frozen-graph", TrackCount: 12}
	entry := parsedIntentEntry{preparedInput: &prepared, outcome: app.ParseOutcome{Backend: "llama", RequestedBackend: "llama"}}
	result, err := interpretationTimedOut(context.Background(), ports.IntentInput{Prompt: "unprepared input must not replace the pinned source"}, entry, core.Automatic, 25*time.Second)
	if err != nil || result.Request.Intent.Count != 12 || result.Request.Intent.PreparedMusicSnapshot != "frozen-graph" || result.Request.Intent.Translation == nil {
		t.Fatalf("prepared input not retained: %+v / %v", result.Request.Intent, err)
	}
	status := result.Status.Parser
	if status.Backend != "rules" || status.RequestedBackend != "llama" || !status.FallbackUsed || status.FallbackReason != "timeout" || status.ReferenceSnapshot != "frozen-identities" || result.Status.Timings[0].Milliseconds != 25000 {
		t.Fatalf("attempted model was claimed as completed, or source/timing lost: %+v", result.Status)
	}
}

func TestInterpretationTimeoutKeepsCompletedParse(t *testing.T) {
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "completed model interpretation", Controls: core.IntentControls{TotalTrackCount: 7}, InterpretationNotes: "completed before deadline observed"}
	entry := parsedIntentEntry{intent: intent, outcome: app.ParseOutcome{Intent: intent, Backend: "llama", RequestedBackend: "llama"}}
	result, err := interpretationTimedOut(context.Background(), ports.IntentInput{}, entry, core.Automatic, time.Second)
	if err != nil || result.Request.Intent.InterpretationNotes != intent.InterpretationNotes || result.Request.Intent.Count != 7 || result.Status.Parser.Backend != "llama" || result.Status.Parser.FallbackUsed {
		t.Fatalf("completed interpretation replaced by fallback: %+v / %v", result, err)
	}
}

func TestInterpretationTimeoutDoesNotFallbackAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := interpretationTimedOut(ctx, ports.IntentInput{Prompt: timeoutJourneyPrompt}, parsedIntentEntry{}, core.Automatic, time.Second)
	if !errors.Is(err, context.Canceled) || result.Request.Intent.Version != 0 || result.Status.Parser.FallbackUsed {
		t.Fatalf("caller cancellation turned into a fallback result: %+v / %v", result, err)
	}
	a := New(newLoadedContainer(t), nil)
	result, err = a.generateFromPrompt(ctx, ports.IntentInput{Prompt: timeoutJourneyPrompt}, nil)
	if !errors.Is(err, context.Canceled) || result.Status.Parser.FallbackUsed || len(a.intentCache.entries) != 0 {
		t.Fatalf("generation cancellation turned into fallback/cache: %+v / %v", result.Status, err)
	}
}
