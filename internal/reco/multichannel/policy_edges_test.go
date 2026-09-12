package multichannel

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestConfigRejectsInvalidBoundsButPreservesDisabledWeights(t *testing.T) {
	invalid := Config{
		EnhancedMERTWeight: -1, EnhancedDSPWeight: -1, EnhancedTransitionWeight: -1,
		SeedAudioBudget: -1, SeedCooccurrenceBudget: -1, TasteClusterBudget: -1, MaxTasteClusters: -1,
		ExplorationPool: -1, ExplorationBudget: -1, ExplorationMinScore: -3, MaxCandidates: -1, ReciprocalRankConstant: -1,
		RetrievalWeight: -1, ListenerWeight: -1, NegativePenalty: -1, ExposurePenalty: -1, NoveltyWeight: -1, ExplorationChance: -1,
		JourneyPositionWeight: -1, ContinuationBudget: -1, SemanticBudget: -1, SemanticMinimumScore: -3, SemanticWeight: -1, SemanticNegativePenalty: -1,
		MMRMinimumLambda: -1, SelectionMinimumRelevance: -3, SelectionRelevanceWindow: -1,
		EmbeddingRedundancyWeight: -1, ArtistConcentrationWeight: -1, AlbumConcentrationWeight: -1,
		SoftArtistSpacingMax: -1, TransitionRelevanceWeight: -1, LocalImprovementPasses: -1, LocalImprovementWindow: -1,
	}
	if got := invalid.normalized(); got != DefaultConfig() {
		t.Fatalf("invalid configuration did not restore safe bounds:\ngot=%+v\nwant=%+v", got, DefaultConfig())
	}
	disabled := DefaultConfig()
	disabled.SemanticWeight, disabled.SemanticNegativePenalty = 0, 0
	disabled.ListenerWeight, disabled.ExposurePenalty, disabled.NegativePenalty = 0, 0, 0
	disabled.LocalImprovementPasses, disabled.SoftArtistSpacingMax = 0, 0
	if got := disabled.normalized(); got != disabled {
		t.Fatalf("explicit zero controls were overridden: %+v", got)
	}
}

func TestCriterionFacetsConfidenceAndProvenanceRemainIndependent(t *testing.T) {
	known := func(value string) core.FeatureValue {
		return core.FeatureValue{Value: value, Missingness: core.FeatureKnown, Confidence: .9, Provenance: []core.FeatureProvenance{{Source: "fixture", SourceID: value, Confidence: .9}}}
	}
	features := core.TrackFeatures{Styles: []core.FeatureValue{known("electronic")}, Moods: []core.FeatureValue{known("relaxing")}, Instrumentation: []core.FeatureValue{known("piano")}, VocalEvidence: known("mixed")}
	for _, tc := range []struct{ kind, value, facet string }{
		{"style", "electronic", "styles"}, {"mood", "relaxing", "moods"}, {"instrumentation", "piano", "instrumentation"}, {"vocal", "vocal", "vocal_evidence"},
	} {
		criterion := core.MusicalCriterion{Kind: tc.kind, Value: tc.value, Scope: "playlist"}
		if !criterionSupported(core.FeatureStoreInfo{SupportedFacets: []string{tc.facet}}, criterion) || criterionSupported(core.FeatureStoreInfo{}, criterion) {
			t.Fatalf("facet capability invented or lost for %s", tc.kind)
		}
		if score := criterionConfidence(features, criterion); score != .9 || len(criterionProvenance(features, criterion)) != 1 {
			t.Fatalf("criterion evidence lost for %s: confidence=%v", tc.kind, score)
		}
		criterion.Value = "not supported"
		if criterionConfidence(features, criterion) != 0 || len(criterionProvenance(features, criterion)) != 0 {
			t.Fatalf("unrelated facet treated as matching %s", tc.kind)
		}
	}
	unknown := core.MusicalCriterion{Kind: "tempo", Value: "120"}
	if criterionSupported(core.FeatureStoreInfo{SupportedFacets: []string{"tags"}}, unknown) || len(criterionValues(features, unknown)) != 0 {
		t.Fatal("unsupported criterion invented")
	}
}

func TestRequiredSemanticEvidenceCannotBeMissingOrOpposing(t *testing.T) {
	store := semanticData()
	criterion := core.MusicalCriterion{Kind: "vocal", Value: "instrumental", Scope: "playlist"}
	for _, id := range []string{"sleepy", "missing"} {
		err := validateRequiredSemanticConstraints(context.Background(), store, []core.TrackRef{{ID: id}}, nil, []core.MusicalCriterion{criterion})
		if !errors.Is(err, core.ErrRequiredTrackConflict) {
			t.Fatalf("required %s lacked evidence but was accepted: %v", id, err)
		}
	}
	if err := validateRequiredSemanticConstraints(context.Background(), store, []core.TrackRef{{ID: "instrumental"}}, nil, []core.MusicalCriterion{criterion}); err != nil {
		t.Fatal("affirmative required evidence rejected", err)
	}
	before := append([]core.FeatureValue(nil), store.features["instrumental"].Styles...)
	_ = criterionValues(store.features["instrumental"], core.MusicalCriterion{Kind: "style"})
	if !reflect.DeepEqual(before, store.features["instrumental"].Styles) {
		t.Fatal("reading criterion values mutated stored evidence")
	}
}
