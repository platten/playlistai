package core

// AcousticCharacteristics is optional archived analysis of a resolved recording.
// Measurements and classifier outputs are not verified genre/constraint evidence.
// Nil numeric fields mean unknown (zero can be a legitimate measurement).
type AcousticCharacteristics struct {
	SchemaVersion int                           `json:"schemaVersion"`
	RecordingID   string                        `json:"recordingId"`
	Source        string                        `json:"source"`
	Submission    int                           `json:"submission"`
	LowStatus     string                        `json:"lowStatus"`  // available, missing, unavailable
	HighStatus    string                        `json:"highStatus"` // available, missing, unavailable
	Low           *AcousticMeasurements         `json:"low,omitempty"`
	Predictions   map[string]AcousticPrediction `json:"predictions,omitempty"`
}

type AcousticMeasurements struct {
	BPM               *float64          `json:"bpm,omitempty"`
	Danceability      *float64          `json:"danceability,omitempty"` // Essentia DFA scale, not 0..1
	AverageLoudness   *float64          `json:"averageLoudness,omitempty"`
	DynamicComplexity *float64          `json:"dynamicComplexity,omitempty"`
	Key               string            `json:"key,omitempty"`
	Scale             string            `json:"scale,omitempty"`
	KeyStrength       *float64          `json:"keyStrength,omitempty"` // estimator strength, not confidence
	AnalyzedSeconds   *float64          `json:"analyzedSeconds,omitempty"`
	Version           map[string]string `json:"version"`
}

type AcousticPrediction struct {
	Value   string             `json:"value"`
	Score   float64            `json:"score"` // upstream probability; not calibrated musical confidence
	Classes map[string]float64 `json:"classes"`
	Version map[string]string  `json:"version"`
}

// IntentComparison keeps source-native scores separate. Supporting/opposing
// classifier predictions are not calibrated EvidenceMatch/EvidenceMismatch.
type IntentComparison struct {
	Clause        AudioClause   `json:"clause"`
	AcousticState string        `json:"acousticState"`           // supporting, opposing, conflicting, unknown
	AcousticScore *float64      `json:"acousticScore,omitempty"` // signed class margin, not probability
	Models        []string      `json:"models,omitempty"`
	PreviewState  EvidenceState `json:"previewState"`
	PreviewScore  *float64      `json:"previewScore,omitempty"` // CLAP cosine, not comparable to class margin
	Conflict      bool          `json:"conflict"`
}
