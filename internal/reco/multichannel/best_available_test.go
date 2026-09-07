package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestBestAvailableReturnsSuggestionsWithoutPromotingUnknown(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := testIntent(3)
	intent.VerificationPolicy = core.BestAvailable
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "previously unseen genre", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Blocked Artist"}}
	pl, err := engine.Build(context.Background(), intent)
	if err != nil || len(pl.Tracks) == 0 || pl.Outcome.State != core.OutcomePartial {
		t.Fatalf("%+v %v", pl, err)
	}
	for _, track := range pl.Tracks {
		if track.Artist == "Blocked Artist" {
			t.Fatal("artist exclusion relaxed")
		}
	}
	if len(pl.Assessments) != len(pl.Tracks) || pl.Assessments[0].State != core.EvidenceUnknown {
		t.Fatal("unknown fit claimed as checked")
	}
	intent.VerificationPolicy = core.VerifiedOnly
	pl, err = engine.Build(context.Background(), intent)
	if err != nil || len(pl.Tracks) != 0 {
		t.Fatal("verified-only mode changed")
	}
}

func TestEvidencePrecedesPersonalizationAndDiversity(t *testing.T) {
	cat := testCatalog()
	known, unknown := candidateFor(cat, "audio", .01), candidateFor(cat, "other", .99)
	known.Scores.Total, unknown.Scores.Total = .01, .99
	known.MusicalFit, unknown.MusicalFit = core.EvidenceMatch, core.EvidenceUnknown
	intent := testIntent(1)
	intent.VerificationPolicy = core.BestAvailable
	selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), []core.Candidate{unknown, known}, ports.SelectionRequest{Intent: intent, Count: 1})
	if err != nil || len(selected.Candidates) != 1 || selected.Candidates[0].Track.ID != "audio" {
		t.Fatalf("unknown track displaced supported fit: %+v %v", selected, err)
	}
}

func TestBestAvailableRejectsKnownMismatchAndFeaturedExclusion(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "wrong")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.bestAvailable = true
	candidates, _, err := o.bestEssential(context.Background(), []core.Candidate{candidateFor(cat, "audio", 1), candidateFor(cat, "other", 1)}, []core.MusicalCriterion{{Kind: "genre", Value: "wanted", Scope: "playlist"}})
	if err != nil || len(candidates) != 1 || candidates[0].Track.ID != "other" {
		t.Fatal("known mismatch accepted")
	}
	intent := testIntent(3)
	intent.Constraints.ArtistsExclude = []string{"Excluded"}
	if o.metadataEligible(core.TrackRef{Artist: "Main", Title: "Song (feat. Excluded)"}, intent) {
		t.Fatal("featured exclusion ignored")
	}
	if !o.metadataEligible(core.TrackRef{Artist: "Main", Title: "Song (feat. Excludedly)"}, intent) {
		t.Fatal("substring excluded an unrelated credit")
	}
}

func TestMixedJourneyEndsWithActualDestination(t *testing.T) {
	cat := testCatalog()
	intent := testIntent(4)
	intent.VerificationPolicy = core.BestAvailable
	intent.Mode = core.ModeJourney
	intent.Destination = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Last Artist", Influence: core.InfluencePositive}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "novel category", Scope: "journey_start"}}
	pl, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(pl.Tracks) < 2 || pl.Tracks[len(pl.Tracks)-1].Artist != "Last Artist" {
		t.Fatalf("destination lost: %+v %v", pl, err)
	}
}
