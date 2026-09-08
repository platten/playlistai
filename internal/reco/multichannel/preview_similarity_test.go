package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestUncalibratedCLAPPlacesJourneyWithoutInventingVerifiedMembership(t *testing.T) {
	cat := testCatalog()
	service, resolver := cachedAudioService(t, cat)
	service.Policy = audio.Policy{}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "journey_start"}, {Kind: "style", Value: "rock", Scope: "journey_end"}}
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Tracks[0].ID != "audio" && result.Tracks[0].ID != "last" {
		t.Fatalf("wrong start: %+v", result.Tracks)
	}
	if result.Tracks[1].ID == "audio" || result.Tracks[1].ID == "last" {
		t.Fatalf("wrong end: %+v", result.Tracks)
	}
	if result.Outcome.State != core.OutcomePartial || !noticeCode(result.Notices, "audio_similarity_ranking") {
		t.Fatal("uncalibrated placement claimed fulfilled")
	}
	for _, assessment := range result.AudioEvidence.Assessments {
		for _, clause := range assessment.Clauses {
			if clause.State != core.EvidenceUnknown {
				t.Fatal("similarity treated as categorical evidence")
			}
		}
	}
	if resolver.calls != 0 {
		t.Fatal("cached embeddings fetched again")
	}
}
