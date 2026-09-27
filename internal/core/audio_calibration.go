package core

import "math"

// AudioSimilarityCalibration is a reviewed threshold for one complete embedding
// space and one literal criterion. Counts and metrics refer to independently
// labeled validation recordings, after freezing the development threshold.
// An absent calibration deliberately cannot establish subjective musical fit.
type AudioSimilarityCalibration struct {
	Version             string  `json:"version"`
	ModelFingerprint    string  `json:"modelFingerprint"`
	Kind                string  `json:"kind"`
	Criterion           string  `json:"criterion"`
	MinimumScore        float64 `json:"minimumScore"`
	DevelopmentSet      string  `json:"developmentSet"`
	ValidationSet       string  `json:"validationSet"`
	ValidationPrecision float64 `json:"validationPrecision"`
	ValidationRecall    float64 `json:"validationRecall"`
	PositiveExamples    int     `json:"positiveExamples"`
	NegativeExamples    int     `json:"negativeExamples"`
}

func (c AudioSimilarityCalibration) ValidForFingerprint(fingerprint, kind, criterion string) bool {
	return fingerprint != "" && c.ModelFingerprint == fingerprint && c.Kind == kind && c.Criterion == criterion &&
		c.Version != "" && c.DevelopmentSet != "" && c.ValidationSet != "" && c.DevelopmentSet != c.ValidationSet &&
		c.PositiveExamples > 0 && c.NegativeExamples > 0 &&
		!math.IsNaN(c.MinimumScore) && c.MinimumScore >= -1 && c.MinimumScore <= 1 &&
		c.ValidationPrecision >= .90 && c.ValidationPrecision <= 1 &&
		c.ValidationRecall > 0 && c.ValidationRecall <= 1
}

func (c AudioSimilarityCalibration) Supports(score float64, fingerprint, kind, criterion string) bool {
	return c.ValidForFingerprint(fingerprint, kind, criterion) && !math.IsNaN(score) && !math.IsInf(score, 0) && score >= c.MinimumScore && score <= 1
}
