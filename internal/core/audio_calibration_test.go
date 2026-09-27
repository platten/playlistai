package core

import (
	"math"
	"testing"
)

func TestSimilarityCalibrationRequiresIndependentEvidenceAndExactSpace(t *testing.T) {
	c := AudioSimilarityCalibration{Version: "synthetic/v1", ModelFingerprint: "space", Kind: "texture", Criterion: "soft piano", MinimumScore: .7, DevelopmentSet: "dev", ValidationSet: "validation", ValidationPrecision: .95, ValidationRecall: .8, PositiveExamples: 20, NegativeExamples: 20}
	if !c.Supports(.8, "space", "texture", "soft piano") {
		t.Fatal("valid synthetic calibration rejected")
	}
	for _, tc := range []struct{ fingerprint, kind, criterion string }{{"other", "texture", "soft piano"}, {"space", "instrumentation", "soft piano"}, {"space", "texture", "piano"}} {
		if c.Supports(.9, tc.fingerprint, tc.kind, tc.criterion) {
			t.Fatal("calibration crossed model or criterion")
		}
	}
	for _, score := range []float64{math.NaN(), math.Inf(1), -.1} {
		if c.Supports(score, "space", "texture", "soft piano") {
			t.Fatal("invalid score accepted")
		}
	}
	c.ValidationSet = c.DevelopmentSet
	if c.ValidForFingerprint("space", "texture", "soft piano") {
		t.Fatal("development reuse accepted as validation")
	}
	c.ValidationSet = "validation"
	c.ValidationPrecision = .89
	if c.ValidForFingerprint("space", "texture", "soft piano") {
		t.Fatal("unvalidated precision accepted")
	}
}
