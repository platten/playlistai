package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestMetadataRequirementsBeforeProgressAndSelection(t *testing.T) {
	for _, policy := range []core.VerificationPolicy{core.BestAvailable, core.VerifiedOnly} {
		t.Run(string(policy), func(t *testing.T) {
			cat := testCatalog()
			service, _ := cachedAudioService(t, cat)
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
			intent := testIntent(2)
			intent.VerificationPolicy = policy
			intent.OriginalDescription = "Music from the chosen album"
			// Keep an actual musical clause so this tests audio-progress gating;
			// album identity alone intentionally no longer triggers CLAP.
			intent.Preferences.Moods = []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}
			intent.HardConstraints = []core.HardConstraint{{Kind: "require_album", Value: "Chosen album"}}
			intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceAlbum, Query: "Chosen album", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: cat.CatalogVersion(), Selected: &core.ResolutionCandidate{Kind: core.ReferenceAlbum, Representatives: []core.WeightedTrack{{TrackID: "audio", Weight: 1}}}}})
			seen := 0
			result, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(track core.TrackRef) {
				seen++
				if track.ID != "audio" {
					t.Errorf("non-album track shown as checked: %s", track.ID)
				}
			}})
			if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "audio" || seen != 1 {
				t.Fatalf("result=%+v checked=%d err=%v", result, seen, err)
			}
			intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
			result, err = engine.Build(context.Background(), intent)
			if err != nil || result.Outcome.State != core.OutcomeNeedsClarification {
				t.Fatalf("required album conflict lost: %+v %v", result.Outcome, err)
			}
		})
	}
}

func TestKnownDateConflictNeverReachesProgress(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	intent.OriginalDescription = "Music released in the twentieth century"
	intent.Temporal = []core.TemporalRequirement{{Basis: "original_release", Scope: "playlist", StartYear: 1901, EndYear: 2000}}
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: core.TrackRef{ID: "audio"}, OriginalReleaseDate: "2010-01-01", IdentityStatus: core.ResolutionResolved}}}
	_, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(track core.TrackRef) {
		if track.ID == "audio" {
			t.Error("conflicting date reached progressive results")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRelativeSimilarityCannotPreferDateIneligibleJourneyStage(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	service.Policy = audio.Policy{}
	intent := testIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "journey_start"}, {Kind: "style", Value: "rock", Scope: "journey_end"}}
	intent.Temporal = []core.TemporalRequirement{{Basis: "original_release", Scope: "journey_end", StartYear: 2000, EndYear: 2020}}
	session, err := service.Begin(context.Background(), intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	meta, _ := cat.Meta("cooc")
	if a, err := session.Check(context.Background(), meta.Ref, false); err != nil || !a.Eligible {
		t.Fatalf("preview: %+v %v", a, err)
	}
	o := &Orchestrator{bestAvailable: true, audioSession: session, knowledge: &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: meta.Ref, OriginalReleaseDate: "1950-01-01", IdentityStatus: core.ResolutionResolved}}}}
	result, _, err := o.filterJourneyStage(context.Background(), []core.Candidate{{Track: meta.Ref}}, intent.EssentialCriteria[0], intent)
	if err != nil || len(result) != 1 {
		t.Fatalf("date-ineligible destination similarity displaced possible start: %+v %v", result, err)
	}
	result, _, err = o.filterJourneyStage(context.Background(), []core.Candidate{{Track: meta.Ref}}, intent.EssentialCriteria[1], intent)
	if err != nil || len(result) != 0 {
		t.Fatal("contradictory date accepted at end")
	}
}

func TestImpossibleJourneyDatesNeverReachProgress(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	service.Policy = audio.Policy{}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	intent.Mode = core.ModeJourney
	intent.OriginalDescription = "A journey entirely within the twentieth century"
	for _, scope := range []string{"journey_start", "journey_end"} {
		intent.Temporal = append(intent.Temporal, core.TemporalRequirement{Basis: "original_release", Scope: scope, StartYear: 1901, EndYear: 2000})
	}
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: core.TrackRef{ID: "audio"}, OriginalReleaseDate: "2010-01-01", IdentityStatus: core.ResolutionResolved}}}
	_, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(track core.TrackRef) {
		if track.ID == "audio" {
			t.Error("track that cannot occupy any dated stage reached progress")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
}
