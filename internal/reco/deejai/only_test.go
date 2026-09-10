package deejai_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/reco/deejai"
)

func TestEngineOnlyPreservesBaselineAndReportsUnverifiedFit(t *testing.T) {
	engine, _ := fakeEngine(t, 60, 5, 42)
	intent := baseIntent()
	intent.Seeds.TrackIDs = []string{"trk3"}
	intent = intent.Normalized()
	intent.Controls.RecommendationMode = core.DeejAIOnly
	baseline, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := deejai.BuildOnly(context.Background(), engine, intent)
	if err != nil || !reflect.DeepEqual(got.Tracks, baseline.Tracks) || got.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("baseline changed: %+v %v", got, err)
	}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}
	intent.VerificationPolicy = core.BestAvailable
	got, err = deejai.BuildOnly(context.Background(), engine, intent)
	if err != nil || len(got.Tracks) == 0 || got.Outcome.State != core.OutcomePartial || len(got.Outcome.Reasons) == 0 || got.AudioEvidence != nil {
		t.Fatalf("unchecked fit reported as fulfilled: %+v %v", got, err)
	}
	intent.VerificationPolicy = core.VerifiedOnly
	got, err = deejai.BuildOnly(context.Background(), engine, intent)
	if err != nil || len(got.Tracks) > 0 || got.Outcome.State != core.OutcomeUnsupported {
		t.Fatal("strict verification bypassed")
	}
}

func TestEngineOnlyBlocksUnsupportedConstraintsAndMissingSeeds(t *testing.T) {
	engine, _ := fakeEngine(t, 60, 5, 42)
	intent := baseIntent().Normalized()
	intent.Controls.RecommendationMode = core.DeejAIOnly
	got, err := deejai.BuildOnly(context.Background(), engine, intent)
	if err != nil || got.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("missing seed: %+v %v", got, err)
	}
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "trk3", Influence: core.InfluencePositive}}
	for _, kind := range []string{"exclude_vocals", "require_style", "require_artist", "exclude_style"} {
		intent.HardConstraints = []core.HardConstraint{{Kind: kind, Value: "fixture"}}
		got, err = deejai.BuildOnly(context.Background(), engine, intent)
		if err != nil || got.Outcome.State != core.OutcomeUnsupported || len(got.Tracks) > 0 {
			t.Fatalf("%s bypassed: %+v %v", kind, got, err)
		}
	}
	intent.HardConstraints = nil
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "instrumental", Influence: core.InfluencePositive}
	got, err = deejai.BuildOnly(context.Background(), engine, intent)
	if err != nil || got.Outcome.State != core.OutcomeUnsupported || len(got.Tracks) > 0 {
		t.Fatal("implicit no-vocals screening bypassed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := deejai.BuildOnly(ctx, engine, intent); err != context.Canceled {
		t.Fatal("cancellation lost")
	}
}
