package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestSubjectiveFitNeedsValidatedLiteralCriterion(t *testing.T) {
	clause := core.AudioClause{Kind: "instrumentation", Text: "soft piano", Scope: "playlist", Essential: true}
	calibration := core.AudioSimilarityCalibration{Version: "synthetic/v1", ModelFingerprint: "space", Kind: clause.Kind, Criterion: clause.Text, MinimumScore: .7, DevelopmentSet: "dev", ValidationSet: "validation", ValidationPrecision: .95, ValidationRecall: .8, PositiveExamples: 20, NegativeExamples: 20}
	b := &automaticBatch{meta: map[string]core.TrackMeta{"track": {Annotations: []core.MetadataAnnotation{{Kind: "instrumentation", Value: "piano", Origin: "embedded_tag"}}}}, assessments: map[string]core.AudioAssessment{"track": {ModelFingerprint: "space", AnalysisID: "preview", Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 20}, Clauses: []core.AudioClauseAssessment{{Clause: clause, Score: .8, ScoreAvailable: true, State: core.EvidenceMatch}}}}}
	if fit := automaticClauseFit(b, "track", clause, .1); fit.State == core.AutomaticStrong {
		t.Fatalf("uncalibrated audio or generic piano admitted description: %+v", fit)
	}
	b.calibrations = []core.AudioSimilarityCalibration{calibration}
	fit := automaticClauseFit(b, "track", clause, .1)
	if fit.State != core.AutomaticStrong || len(fit.Signals) != 1 || fit.Signals[0].Calibration == nil || fit.Signals[0].Coverage.CoveredSeconds != 20 {
		t.Fatalf("validated audio lost fit or provenance: %+v", fit)
	}
	b.calibrations[0].ModelFingerprint = "incompatible"
	if automaticClauseFit(b, "track", clause, .1).State == core.AutomaticStrong {
		t.Fatal("incompatible calibration admitted recording")
	}
	b.calibrations[0] = calibration
	b.calibrations[0].Criterion = "piano"
	if automaticClauseFit(b, "track", clause, .1).State == core.AutomaticStrong {
		t.Fatal("generic criterion calibration admitted description")
	}
}

func TestAutomaticDoesNotFillUnknownHardDescriptiveExclusion(t *testing.T) {
	for _, prompt := range []string{"no pounding drums", "without distorted guitars", "not sleepy", "no martelé drums or distorted guitars", "no ringing guitar tones or pounding drums", "no pounding drums or martelé guitar tones"} {
		t.Run(prompt, func(t *testing.T) {
			catalog, intent, retriever := automaticFixture(t, 1)
			intent.OriginalDescription = prompt
			intent = lexicon.Reconcile(intent, lexicon.Extract(prompt))
			automaticSupport(catalog, intent, "0", "house")
			result, err := NewAutomatic(catalog, nil, retriever, DefaultConfig()).Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Tracks) != 0 {
				t.Fatalf("filled unknown hard exclusion: %+v", result)
			}
		})
	}
}
