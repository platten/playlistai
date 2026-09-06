package evaluation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

func TestRunnerSeparatesSyntheticEvidenceAndWritesBlindPairs(t *testing.T) {
	t.Parallel()
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "seed", Display: "Seed - Start", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "a", Display: "A - One", Audio: []float32{.9, .1}, Track: []float32{.8, .2}},
		fakes.CatalogTrack{ID: "b", Display: "B - Two", Audio: []float32{.7, .3}, Track: []float32{.6, .4}},
		fakes.CatalogTrack{ID: "c", Display: "C - Three", Audio: []float32{0, 1}, Track: []float32{0, 1}})
	count := 2
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := make([]RecommendationCase, 5)
	tags := []string{"cold_start", "multiple_taste", "niche", "non_latin", "ambiguous_reference"}
	for i := range cases {
		cases[i] = RecommendationCase{ID: tags[i], ListenerID: "listener", OccurredAt: base.Add(time.Duration(i) * 24 * time.Hour), Intent: core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "42", References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: count, AudioWeight: .5, CooccurrenceWeight: .5}}, Relevance: map[string]float64{"a": 3, "b": 2}, Tags: []string{tags[i]}}
	}
	dataset := Dataset{Version: ContractVersion, Name: "synthetic-harness", Evidence: EvidenceSynthetic, RecommendationCases: cases, IntentCases: []IntentCase{{ID: "negation", Prompt: "ambient but not sleepy", Expected: IntentLabels{NegativePreferences: []string{"sleepy"}}}}, ResolutionCases: []ResolutionCase{{ID: "exact", Reference: core.IntentReference{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}, ExpectedStatus: core.ResolutionResolved, AcceptableEntityIDs: []string{"seed"}}}}
	runner := Runner{Catalog: cat, Resolver: cat, Similarity: fakes.NewSimilarityEngine(cat), Parser: rules.New(), K: 2}
	report, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatal(err)
	}
	if report.SelectedParameters != nil || len(report.HeldOutTest) != 6 || report.Cohorts["non_latin"] != 1 {
		t.Fatalf("report evidence separation/ablations=%+v", report)
	}
	if len(report.Limitations) == 0 {
		t.Fatal("synthetic limitation missing")
	}
	for _, variant := range report.HeldOutTest {
		if len(variant.Cases) != 1 || variant.Cases[0].Generation.AlgorithmVersion == "" {
			t.Fatalf("reproducibility missing: %+v", variant)
		}
	}
	blind, key := filepath.Join(t.TempDir(), "blind.json"), filepath.Join(t.TempDir(), "key.json")
	if err := WriteBlindComparison(report, dataset, cat, "blended_walk", "diversity_sequencing", "7", blind, key); err != nil {
		t.Fatal(err)
	}
	alpha := parametersFromConfig("alpha", multichannel.DefaultConfig())
	zeta := alpha
	zeta.Name = "zeta"
	dataset.Evidence = EvidenceJudged
	dataset.TuningGrid = []ParameterSet{zeta, alpha}
	tuned, err := runner.Run(context.Background(), dataset)
	if err != nil {
		t.Fatal(err)
	}
	if tuned.SelectedParameters == nil || tuned.SelectedParameters.Name != "alpha" || len(tuned.TuningCandidates) != 2 {
		t.Fatalf("development tuning was not auditable or deterministic: %+v", tuned.SelectedParameters)
	}
}
