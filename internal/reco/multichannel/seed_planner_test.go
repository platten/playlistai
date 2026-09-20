package multichannel

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func seedPlannerFixture() (*Orchestrator, core.MusicIntent) {
	cat, intent := localPriorityFixture()
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
	o.enhanced = true
	o.bestAvailable = true
	o.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "unknown", "pack:fixture:local:one", "provider:two"))}}
	return o, intent
}

func TestGroundedSeedsRespectEvidenceExclusionsAndExplicitReferences(t *testing.T) {
	o, intent := seedPlannerFixture()
	intent.Constraints.ArtistsExclude = []string{"Local"}
	got, seeds, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || len(seeds) != 1 || seeds[0].Track.ID != "provider:two" || len(got.InferredAnchors) != 1 {
		t.Fatalf("invalid grounded seeds: %+v %+v %v", got.InferredAnchors, seeds, err)
	}
	if len(got.RequiredTracks) != 0 || got.Start != nil || len(got.References) != 0 {
		t.Fatal("inferred starting aid rewrote explicit intent")
	}
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "provider:two", Influence: core.InfluencePositive}}
	untouched, seeds, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || len(seeds) != 0 || !reflect.DeepEqual(untouched, intent) {
		t.Fatal("explicit reference overwritten")
	}
}

func TestGroundedSeedsAreSourceNeutralAndRemainOutputCandidates(t *testing.T) {
	o, intent := seedPlannerFixture()
	got, seeds, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || len(seeds) != 2 {
		t.Fatalf("local and outside were not both eligible: %+v %v", seeds, err)
	}
	seen := map[string]bool{}
	for _, seed := range seeds {
		seen[seed.Track.ID] = true
	}
	if !seen["pack:fixture:local:one"] || !seen["provider:two"] {
		t.Fatal("source membership filtered a fitting seed")
	}
	e := newEligibility(got, resolvedReferenceTracks(o.cat, got), nil)
	permitGroundedSeeds(e, seeds)
	eligible, err := e.filter(context.Background(), seeds, false)
	if err != nil || len(eligible) != 2 {
		t.Fatal("automatic seeds excluded from normal content")
	}
	e.excludeRecent([]core.TrackRef{seeds[0].Track})
	eligible, err = e.filter(context.Background(), seeds, false)
	if err != nil || len(eligible) != 1 {
		t.Fatal("seed promotion bypassed recent exclusion")
	}
	_, again, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || !reflect.DeepEqual(seeds, again) {
		t.Fatal("seed selection is not deterministic")
	}
}

func TestGroundedSeedsRequireKnownRequestedDatesAndHonorCancellation(t *testing.T) {
	o, intent := seedPlannerFixture()
	intent.Temporal = []core.TemporalRequirement{{Basis: "original_release", StartYear: 1990, EndYear: 1999, Scope: "playlist"}}
	_, seeds, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || len(seeds) != 0 {
		t.Fatal("unknown date treated as verified decade fit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := o.planGroundedSeeds(ctx, intent, ports.RecommendationRequest{}, 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestGroundedSeedNeighborhoodBreaksOnlyRelevanceTies(t *testing.T) {
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "isolated", Display: "A - Isolated", Audio: []float32{-1, 0}}, fakes.CatalogTrack{ID: "connected", Display: "B - Connected", Audio: []float32{1, 0}}, fakes.CatalogTrack{ID: "neighbor", Display: "C - Neighbor", Audio: []float32{.99, .01}})
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	pool := candidatesForTracks(refs(cat, "isolated", "connected", "neighbor"))
	if err := o.preferViableSeedNeighborhoods(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if pool[0].Track.ID == "isolated" {
		t.Fatal("isolated seed won equal relevance despite viable neighbors")
	}
	pool = candidatesForTracks(refs(cat, "isolated", "connected", "neighbor"))
	pool[0].Scores.Total = 1
	if err := o.preferViableSeedNeighborhoods(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if pool[0].Track.ID != "isolated" {
		t.Fatal("neighborhood overrode request relevance")
	}
}

func TestGroundedSeedSoftSimilarityAndProminenceRemainUnknown(t *testing.T) {
	o, intent := seedPlannerFixture()
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "instrumentation", Value: "piano"}}
	intent.Preferences.Instrumentation = []core.IntentPreference{{Value: "piano", Degree: "mostly", Influence: core.InfluencePositive}}
	o.packedAssessments = map[string]core.AudioAssessment{"provider:two": {TrackID: "provider:two", AnalysisID: "fixture", Clauses: []core.AudioClauseAssessment{{Clause: core.AudioClause{Kind: "instrumentation", Text: "piano", Essential: true}, Score: .8, ScoreAvailable: true, State: core.EvidenceUnknown}}}}
	got, seeds, err := o.planGroundedSeeds(context.Background(), intent, ports.RecommendationRequest{}, 42)
	if err != nil || len(seeds) != 1 || got.InferredAnchors[0].Suitability.State != core.EvidenceUnknown {
		t.Fatalf("similarity became categorical piano prominence: %+v %+v %v", got.InferredAnchors, seeds, err)
	}
	got.AnchorAttempts = append([]core.InferredAnchor(nil), got.InferredAnchors...)
	_, replay, err := o.planGroundedSeeds(context.Background(), got, ports.RecommendationRequest{}, 42)
	if err != nil || len(replay) != 1 || replay[0].Track.ID != seeds[0].Track.ID {
		t.Fatalf("replay lost selectable generated anchor: %+v %v", replay, err)
	}
}

func TestArtistOnlyJourneyUsesNeighborhoodRepresentatives(t *testing.T) {
	cat := seedArtistCatalog{fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a-wrong", Display: "Radiohead - Isolated", Audio: []float32{-1, 0}}, fakes.CatalogTrack{ID: "a-right", Display: "Radiohead - Center", Audio: []float32{1, 0}}, fakes.CatalogTrack{ID: "a-neighbor", Display: "Radiohead - Neighbor", Audio: []float32{.99, .01}})}
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Radiohead", TrackID: "a-wrong", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: "Radiohead", Artist: "Radiohead", Representatives: []core.WeightedTrack{{TrackID: "a-wrong", Weight: 1}}}}}
	intent := core.MusicIntent{Mode: core.ModeJourney, Start: &ref}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
	o.enhanced = true
	got, err := o.refineArtistRepresentatives(context.Background(), intent)
	if err != nil || got.Start.TrackID == "a-wrong" || got.Start.Query != "Radiohead" {
		t.Fatalf("artist-only endpoint kept isolated first-row seed: %+v %v", got.Start, err)
	}
}

type seedArtistCatalog struct{ *fakes.Catalog }

func (c seedArtistCatalog) ArtistRecordings(_ context.Context, artist string) ([]core.TrackRef, error) {
	var out []core.TrackRef
	for _, id := range []string{"a-wrong", "a-right", "a-neighbor", "b-wrong", "b-right", "b-neighbor"} {
		m, _ := c.Meta(id)
		if m.Ref.Artist == artist {
			out = append(out, m.Ref)
		}
	}
	return out, nil
}
func (c seedArtistCatalog) CriterionEvidence(_ context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if id == "a-right" && criterion.Value == "ambient" || id == "b-right" && criterion.Value == "industrial" {
		return core.EvidenceMatch
	}
	return core.EvidenceMismatch
}

func TestArtistRepresentativeRefinementRespectsJourneyStagesAndIdentity(t *testing.T) {
	cat := seedArtistCatalog{fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a-wrong", Display: "Radiohead - Wrong"}, fakes.CatalogTrack{ID: "a-right", Display: "Radiohead - Right"}, fakes.CatalogTrack{ID: "b-wrong", Display: "Marilyn Manson - Wrong"}, fakes.CatalogTrack{ID: "b-right", Display: "Marilyn Manson - Right"})}
	ref := func(artist, id string) core.IntentReference {
		return core.IntentReference{Kind: core.ReferenceArtist, Query: artist, TrackID: id, Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: artist, Artist: artist, Representatives: []core.WeightedTrack{{TrackID: id, Weight: 1}}}}}
	}
	start, end := ref("Radiohead", "a-wrong"), ref("Marilyn Manson", "b-wrong")
	intent := core.MusicIntent{Mode: core.ModeJourney, Start: &start, Destination: &end, Journey: core.JourneyPlan{Waypoints: []core.IntentReference{start, end}}, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "ambient", Scope: "journey_start"}, {Kind: "genre", Value: "industrial", Scope: "journey_end"}}}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
	o.enhanced = true
	got, err := o.refineArtistRepresentatives(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if got.Start.TrackID != "a-right" || got.Destination.TrackID != "b-right" || got.Journey.Waypoints[0].Query != "Radiohead" || got.Journey.Waypoints[1].Query != "Marilyn Manson" {
		t.Fatalf("journey scope/identity lost: %+v", got)
	}
	if start.TrackID != "a-wrong" || start.Resolution.Selected.Representatives[0].TrackID != "a-wrong" {
		t.Fatal("input resolution mutated")
	}
	start.Kind = core.ReferenceTrack
	intent.Start = &start
	got, err = o.refineArtistRepresentatives(context.Background(), intent)
	if err != nil || got.Start.TrackID != "a-wrong" {
		t.Fatal("explicit recording replaced")
	}
}
