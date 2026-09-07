package multichannel

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestParsedCategoryAndSeedCannotFulfillWithoutEvidence(t *testing.T) {
	intent, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "electronic music like Seed Artist"})
	intent.Controls.TotalTrackCount = 3
	cat := testCatalog()
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomeUnsupported || len(playlist.Tracks) != 0 {
		t.Fatalf("category+seed bypassed essential evidence: %+v, %v", playlist, err)
	}
}

func TestAmbientElectronicaFailsHonestlyWithoutSemanticEvidence(t *testing.T) {
	intent, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "ambient electronica"})
	cat := testCatalog()
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomeUnsupported || len(playlist.Tracks) != 0 || !outcomeReason(playlist, "unsupported_essential_criterion") {
		t.Fatalf("ambient electronica reached seed resolution instead of an evidence outcome: %+v, %v", playlist, err)
	}
}

func TestParsedCategoryExclusionRetrievesWithoutSpuriousArtistResolution(t *testing.T) {
	cat := testCatalog()
	sem := correctnessFeatures(cat)
	sem.positive = []core.SemanticHit{{TrackID: "audio", Score: .9}, {TrackID: "other", Score: .9}, {TrackID: "cooc", Score: .9}}
	intent, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "electronic music, no rock"})
	intent.Controls.TotalTrackCount = 1
	playlist, err := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomeFulfilled || len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "audio" {
		t.Fatalf("category exclusion failed: %+v, %v", playlist, err)
	}
	for _, track := range playlist.Tracks {
		if !core.SemanticConstraintSatisfied(sem.features[track.ID], core.HardConstraint{Kind: "exclude_style", Value: "rock"}) {
			t.Fatal("no-rock constraint was violated")
		}
	}
}

func TestUncertainRockCannotPassFeatureOnlyExclusion(t *testing.T) {
	cat := testCatalog()
	sem := correctnessFeatures(cat)
	feature := sem.features["audio"]
	rock := reliableStyle("audio", "rock")
	rock.Confidence = .5
	feature.Styles = append(feature.Styles, rock)
	sem.features["audio"] = feature
	intent := testIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	playlist, err := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, nil, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomePartial || len(playlist.Tracks) != 0 {
		t.Fatalf("uncertain no-rock evidence admitted: %+v, %v", playlist, err)
	}
}

func categoryRegressionFixture() (*fakes.Catalog, *semanticFixture, core.MusicIntent) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "e1", Display: "A - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "e2", Display: "A - Two", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "r1", Display: "B - Three", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "r2", Display: "B - Four", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)
	sem := &semanticFixture{
		info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), SupportedFacets: []string{"styles"}},
		features: map[string]core.TrackFeatures{
			"e1": completeStyleFeature("e1", "electronic"), "e2": completeStyleFeature("e2", "electronic"),
			"r1": completeStyleFeature("r1", "rock"), "r2": completeStyleFeature("r2", "rock"),
		},
		positive: []core.SemanticHit{{TrackID: "e1", Score: .9}, {TrackID: "e2", Score: .9}, {TrackID: "r1", Score: .9}, {TrackID: "r2", Score: .9}},
	}
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Mode: core.ModeJourney, Seed: "46",
		EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "journey_start"}, {Kind: "style", Value: "rock", Scope: "journey_end"}},
		Controls:          core.IntentControls{TotalTrackCount: 4, AudioWeight: .5, CooccurrenceWeight: .5},
	}.Normalized()
	return cat, sem, intent
}

func TestCategoryJourneyCannotBreakHardSpacingAfterSequencing(t *testing.T) {
	cat, sem, intent := categoryRegressionFixture()
	intent.HardConstraints = []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true", Supported: true}}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomePartial || len(playlist.Tracks) != 2 {
		t.Fatalf("expected two-track safe partial journey: %+v, %v", playlist, err)
	}
	for index := 1; index < len(playlist.Tracks); index++ {
		if sameArtist(playlist.Tracks[index-1], playlist.Tracks[index]) {
			t.Fatalf("hard spacing violated: %+v", playlist.Tracks)
		}
	}
	assertCategoryDirection(t, playlist, sem)
	again, err := engine.Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(playlist, again) {
		t.Fatalf("category sequencing not deterministic: %v", err)
	}
}

func TestRequiredRockTrackDoesNotDisableCategoryDirection(t *testing.T) {
	cat, sem, intent := categoryRegressionFixture()
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "r1", Influence: core.InfluencePositive}}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomeFulfilled || len(playlist.Tracks) != 4 || !trackIDSet(playlist.Tracks)["r1"] {
		t.Fatalf("required category journey failed: %+v, %v", playlist, err)
	}
	assertCategoryDirection(t, playlist, sem)
	intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "e1", Influence: core.InfluencePositive})
	conflict, err := engine.Build(context.Background(), intent)
	if err != nil || conflict.Outcome.State != core.OutcomeNeedsClarification || len(conflict.Tracks) != 0 || !outcomeReason(conflict, "required_track_order_conflict") {
		t.Fatalf("contradictory required order was silently accepted: %+v, %v", conflict, err)
	}
}

func TestCategoryJourneyWithExplicitReferenceWaypointsStillOrdersDirection(t *testing.T) {
	cat, sem, intent := categoryRegressionFixture()
	intent.Controls.TotalTrackCount = 2 // reference waypoints are not required output
	sem.positive = []core.SemanticHit{{TrackID: "r2", Score: .99}, {TrackID: "e2", Score: .7}}
	intent.Journey.Waypoints = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "e1", Influence: core.InfluencePositive}, {Kind: core.ReferenceTrack, TrackID: "r1", Influence: core.InfluencePositive}}
	playlist, err := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || playlist.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("waypoint journey failed: %+v, %v", playlist, err)
	}
	assertCategoryDirection(t, playlist, sem)
}

func assertCategoryDirection(t *testing.T, playlist core.Playlist, sem *semanticFixture) {
	t.Helper()
	stages := core.JourneyCriteria(playlist.Intent.EssentialCriteria)
	states := make([][]core.EvidenceState, len(playlist.Tracks))
	for index, track := range playlist.Tracks {
		states[index] = make([]core.EvidenceState, len(stages))
		for stage, criterion := range stages {
			states[index][stage] = core.CriterionEvidence(sem.features[track.ID], criterion)
		}
	}
	if got := core.JourneySequenceViolations(states, len(stages)); got != 0 {
		t.Fatalf("category direction violated: %d; %+v", got, playlist.Tracks)
	}
}
