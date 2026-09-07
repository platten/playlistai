package multichannel

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestEssentialStyleChangesEligibilityWithSameSeedAndControls(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{
		info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), FeatureVersion: "reviewed/v1", ModelRevision: "human", SupportedFacets: []string{"styles"}},
		features: map[string]core.TrackFeatures{
			"audio": completeStyleFeature("audio", "electronic"),
			"cooc":  completeStyleFeature("cooc", "rock & roll"),
		},
	}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	base := testIntent(1)
	base.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	electronic, err := engine.Build(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	rockIntent := base
	rockIntent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "rock & roll"}}
	rock, err := engine.Build(context.Background(), rockIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(electronic.Tracks) != 1 || electronic.Tracks[0].ID != "audio" || len(rock.Tracks) != 1 || rock.Tracks[0].ID != "cooc" {
		t.Fatalf("style did not change eligibility: electronic=%+v rock=%+v", electronic.Tracks, rock.Tracks)
	}
}

func TestUnsupportedRequireStyleCannotReportFulfilledPlaylist(t *testing.T) {
	intent := testIntent(3)
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_style", Value: "electronic"}}
	playlist, err := New(testCatalog(), fakes.NewSimilarityEngine(testCatalog()), testCatalog(), DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeUnsupported || len(playlist.Tracks) != 0 {
		t.Fatalf("unsupported strict style was presented as fulfilled: %+v", playlist)
	}
}

func TestMissingSemanticEvidenceUsesFixedRequestDenominator(t *testing.T) {
	cat := testCatalog()
	candidates := []core.Candidate{candidateFor(cat, "audio", 1), candidateFor(cat, "cooc", 1)}
	candidates[0].Scores.SemanticMatch = .5
	candidates[0].Available.SemanticMatch = true
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Controls: core.IntentControls{TotalTrackCount: 2}}
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Track.ID != "audio" || ranked[0].Scores.Total <= ranked[1].Scores.Total {
		t.Fatalf("missing semantic evidence gained a normalization advantage: %+v", ranked)
	}
}

func TestExplorationCandidatesReceiveNegativeSemanticScoring(t *testing.T) {
	cat := semanticCatalog()
	sem := semanticData()
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Preferences: core.SemanticPreferences{Styles: []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}, {Value: "sleepy", Influence: core.InfluenceNegative}}}}.Normalized()
	candidates := []core.Candidate{{Track: mustMeta(t, cat, "sleepy"), Sources: []core.RetrievalEvidence{{Channel: ChannelExploration}}}}
	scored, _, _, err := engine.scoreSemanticUnion(context.Background(), candidates, intent)
	if err != nil {
		t.Fatal(err)
	}
	if !scored[0].Available.SemanticNegativeMatch || scored[0].Scores.SemanticNegativeMatch <= 0 {
		t.Fatalf("exploration candidate escaped negative scoring: %+v", scored[0])
	}
}

func TestStyleExclusionRequiresCompleteAbsenceEvidence(t *testing.T) {
	feature := completeStyleFeature("track", "electronic")
	feature.FacetCoverage = nil
	constraint := []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	if constraintsSatisfied(feature, constraint) {
		t.Fatal("an unrelated known tag was treated as proof that rock is absent")
	}
	feature.FacetCoverage = []string{"styles"}
	if !constraintsSatisfied(feature, constraint) {
		t.Fatal("complete reviewed style evidence should establish the exclusion")
	}
}

func TestStyleAliasesAndHierarchyAreConservative(t *testing.T) {
	cases := []struct {
		want, actual string
		match        bool
	}{
		{"electronic", "electronica", true},
		{"electronic", "techno", true},
		{"rock", "rock & roll", true},
		{"rock", "dance-rock", true},
		{"electronic", "dance-rock", false},
		{"rock", "electronic", false},
	}
	for _, item := range cases {
		if got := styleMatches(canonicalStyle(item.want), canonicalStyle(item.actual)); got != item.match {
			t.Errorf("styleMatches(%q, %q) = %v, want %v", item.want, item.actual, got, item.match)
		}
	}
}

func TestFeatureOnlySidecarEnforcesEssentialCriteria(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{
		info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), FeatureVersion: "feature-only/v1", ModelRevision: "human", SupportedFacets: []string{"styles"}},
		features: map[string]core.TrackFeatures{
			"audio": completeStyleFeature("audio", "electronic"),
			"cooc":  completeStyleFeature("cooc", "rock"),
		},
	}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "audio" || playlist.Outcome.State != core.OutcomePartial {
		t.Fatalf("feature-only eligibility was disconnected: %+v", playlist)
	}
}

func TestElectronicMusicWithoutEvidenceReturnsUnsupportedNotRock(t *testing.T) {
	cat := testCatalog()
	intent := testIntent(4)
	intent.References = nil
	intent.InferredAnchors = []core.InferredAnchor{{
		Reference: core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive},
		Role:      "retrieval proposal", Reason: "model proposal", Suitability: core.AnchorSuitability{State: core.EvidenceUnknown},
	}}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeUnsupported || len(playlist.Tracks) != 0 || !outcomeReason(playlist, "unsupported_essential_criterion") {
		t.Fatalf("unverified category was presented as a successful playlist: %+v", playlist)
	}
}

func TestElectronicNoRockRequiresAffirmativeCategoryAndExclusionEvidence(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(4)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock", Supported: false}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "audio" || playlist.Outcome.State != core.OutcomePartial {
		t.Fatalf("electronic/no-rock eligibility = %+v", playlist)
	}
}

func TestEssentialCategoryAndSameExclusionNeedsClarification(t *testing.T) {
	intent := testIntent(4)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "rock & roll"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock", Supported: false}}
	playlist, err := New(testCatalog(), fakes.NewSimilarityEngine(testCatalog()), testCatalog(), DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeNeedsClarification || !outcomeReason(playlist, "essential_exclusion_conflict") {
		t.Fatalf("contradictory request was not surfaced: %+v", playlist)
	}
}

func TestElectronicWithRockInfluencePreservesDeliberateHybrid(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	intent.Preferences.Styles = []core.IntentPreference{
		{Value: "electronic", Influence: core.InfluencePositive, Explicit: true},
		{Value: "rock", Influence: core.InfluencePositive, Explicit: true},
	}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	seen := trackIDSet(playlist.Tracks)
	if len(playlist.Tracks) != 2 || !seen["audio"] || !seen["other"] || seen["cooc"] {
		t.Fatalf("deliberate electronic/rock hybrid was collapsed or pure rock leaked: %+v", playlist)
	}
}

func TestHistoricalRockTasteAndMaximumExplorationCannotOverrideElectronic(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	intent.Controls.Discovery = 1
	intent.Controls.ArtistDiversity = 1
	profile := core.TasteProfile{Positive: core.EmbeddingAffinity{Audio: []float32{-1, 0}, Cooccurrence: []float32{0, 1}}, Clusters: []core.TasteCluster{{ID: "rock", Weight: 1, Affinity: core.EmbeddingAffinity{Audio: []float32{-1, 0}, Cooccurrence: []float32{0, 1}}}}}
	playlist, err := engine.BuildWithProfile(context.Background(), intent, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 1 || playlist.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("unexpected maximum-discovery result: %+v", playlist)
	}
	feature, ok := features.features[playlist.Tracks[0].ID]
	if !ok || styleState(feature, "electronic") != core.EvidenceMatch {
		t.Fatalf("personalization or exploration overrode essential eligibility: %+v", playlist)
	}
}

func TestRequiredTrackConflictWithEssentialCategoryNeedsClarification(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "cooc", Influence: core.InfluencePositive}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeNeedsClarification || len(playlist.Tracks) != 0 || !outcomeReason(playlist, "required_track_essential_conflict") {
		t.Fatalf("required/category conflict was not explicit: %+v", playlist)
	}
}

func TestMatchingRequiredTrackCanAppearWithEssentialCategory(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "audio", Influence: core.InfluencePositive}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) == 0 || playlist.Tracks[0].ID != "audio" || playlist.Outcome.State == core.OutcomeNeedsClarification {
		t.Fatalf("eligible required track was rejected: %+v", playlist)
	}
}

func TestMissingEssentialQueryVocabularyReturnsUnsupported(t *testing.T) {
	cat := semanticCatalog()
	sem := semanticData()
	sem.coverage = &core.QueryCoverage{Matched: []string{"music"}, Unmatched: []string{"electronic"}, Complete: false}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, nil, sem, DefaultConfig())
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Seed: "44",
		EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}},
		Controls:          core.IntentControls{TotalTrackCount: 2, AudioWeight: .5, CooccurrenceWeight: .5},
	}.Normalized()
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeUnsupported || len(playlist.Tracks) != 0 || !outcomeReason(playlist, "essential_query_uncovered") {
		t.Fatalf("dropped defining vocabulary was accepted: %+v", playlist)
	}
}

func TestAmbiguousExplicitReferenceNeedsClarification(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "one", Display: "First Artist - Collision", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "two", Display: "Second Artist - Collision", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, References: []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Collision", Influence: core.InfluencePositive}},
		Controls: core.IntentControls{TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5}, Seed: "45",
	}.Normalized()
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if playlist.Outcome.State != core.OutcomeNeedsClarification || !outcomeReason(playlist, "ambiguous_reference") {
		t.Fatalf("ambiguous explicit reference was guessed: %+v", playlist)
	}
}

func TestInferredAnchorResolutionAndSuitabilityRemainSeparate(t *testing.T) {
	cat := testCatalog()
	features := correctnessFeatures(cat)
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	intent := testIntent(1)
	intent.References = nil
	intent.InferredAnchors = []core.InferredAnchor{{
		Reference: core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive},
		Role:      "retrieval proposal", Reason: "model proposal", Suitability: core.AnchorSuitability{State: core.EvidenceUnknown},
	}}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	anchor := playlist.Intent.InferredAnchors[0]
	if anchor.Reference.Resolution == nil || anchor.Reference.Resolution.Status != core.ResolutionResolved || anchor.Suitability.State != core.EvidenceMismatch {
		t.Fatalf("entity confidence and musical suitability were conflated: %+v", anchor)
	}
	if playlist.Outcome.State != core.OutcomeUnsupported || !outcomeReason(playlist, "no_suitable_anchor") {
		t.Fatalf("unsuitable anchor still steered a playlist: %+v", playlist)
	}
}

func TestElectronicToRockJourneyReservesStagesAndOrdersDirection(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "electronic", Display: "Electronic Artist - Start", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "hybrid", Display: "Hybrid Artist - Bridge", Audio: []float32{.7, .7}, Track: []float32{.7, .7}},
		fakes.CatalogTrack{ID: "rock", Display: "Rock Artist - End", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
	sem := &semanticFixture{
		info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), FeatureVersion: "journey/v1", ModelRevision: "human", SupportedFacets: []string{"styles"}},
		features: map[string]core.TrackFeatures{
			"electronic": completeStyleFeature("electronic", "electronic"),
			"hybrid": func() core.TrackFeatures {
				feature := completeStyleFeature("hybrid", "electronic")
				feature.Styles = append(feature.Styles, reliableStyle("hybrid", "dance-rock"))
				return feature
			}(),
			"rock": completeStyleFeature("rock", "rock & roll"),
		},
		positive: []core.SemanticHit{{TrackID: "electronic", Score: .95}, {TrackID: "hybrid", Score: .9}, {TrackID: "rock", Score: .85}},
	}
	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Mode: core.ModeJourney, Seed: "46",
		EssentialCriteria: []core.MusicalCriterion{
			{Kind: "style", Value: "electronic", Scope: "journey_start"},
			{Kind: "style", Value: "rock", Scope: "journey_end"},
		},
		Controls: core.IntentControls{TotalTrackCount: 3, AudioWeight: .5, CooccurrenceWeight: .5, TransitionSmoothness: 1},
	}.Normalized()
	playlist, err := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 3 || playlist.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("journey total/outcome = %+v", playlist)
	}
	first, last := sem.features[playlist.Tracks[0].ID], sem.features[playlist.Tracks[len(playlist.Tracks)-1].ID]
	if styleState(first, "electronic") != core.EvidenceMatch || styleState(last, "rock") != core.EvidenceMatch {
		t.Fatalf("journey direction was not preserved: %+v", playlist.Tracks)
	}
	short := intent
	short.Controls.TotalTrackCount = 1
	short = short.Normalized()
	result, err := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig()).Build(context.Background(), short)
	if err != nil || result.Outcome.State != core.OutcomeNeedsClarification || !outcomeReason(result, "journey_count_too_short") {
		t.Fatalf("short journey = %+v, %v", result, err)
	}
}

func correctnessFeatures(cat *fakes.Catalog) *semanticFixture {
	features := make(map[string]core.TrackFeatures, cat.Len())
	for index := 0; index < cat.Len(); index++ {
		id := cat.ID(index)
		style := "rock & roll"
		switch id {
		case "audio":
			style = "electronic"
		case "other":
			feature := completeStyleFeature(id, "electronic")
			feature.Styles = append(feature.Styles, reliableStyle(id, "dance-rock"))
			features[id] = feature
			continue
		}
		features[id] = completeStyleFeature(id, style)
	}
	return &semanticFixture{
		info:     core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), FeatureVersion: "reviewed/v1", ModelRevision: "human", SupportedFacets: []string{"styles"}},
		features: features,
	}
}

func reliableStyle(id, style string) core.FeatureValue {
	return core.FeatureValue{Value: style, Missingness: core.FeatureKnown, Confidence: .95, Provenance: []core.FeatureProvenance{{Source: "reviewed-pilot", SourceID: id, SourceVersion: "1", Confidence: .95}}}
}

func trackIDSet(tracks []core.TrackRef) map[string]bool {
	result := map[string]bool{}
	for _, track := range tracks {
		result[track.ID] = true
	}
	return result
}

func outcomeReason(playlist core.Playlist, code string) bool {
	for _, reason := range playlist.Outcome.Reasons {
		if reason.Code == code || strings.Contains(reason.Code, code) {
			return true
		}
	}
	return false
}

func completeStyleFeature(id, style string) core.TrackFeatures {
	return core.TrackFeatures{
		SchemaVersion: 3, CatalogVersion: "fake:v1", TrackID: id, FacetCoverage: []string{"styles"},
		Styles: []core.FeatureValue{{Value: style, Missingness: core.FeatureKnown, Confidence: .95, Provenance: []core.FeatureProvenance{{Source: "reviewed-pilot", SourceID: id, SourceVersion: "1", Confidence: .95}}}},
	}
}

func mustMeta(t *testing.T, cat ports.Catalog, id string) core.TrackRef {
	t.Helper()
	meta, ok := cat.Meta(id)
	if !ok {
		t.Fatalf("missing catalog track %q", id)
	}
	return meta.Ref
}
