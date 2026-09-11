package core

// AudioRepresentationIdentity identifies an audio-only vector space. Every field
// participates in compatibility; these vectors are never CLAP text embeddings.
type AudioRepresentationIdentity struct {
	Model         string `json:"model"`
	Revision      string `json:"revision"`
	Preprocessing string `json:"preprocessing"`
	Runtime       string `json:"runtime"`
	Dimension     int    `json:"dimension"`
	WeightsSHA256 string `json:"weightsSha256"`
	Pooling       string `json:"pooling"`
}

// Segment times describe observed preview samples, excluding model padding.
type AudioRepresentationSegment struct {
	StartSeconds float64   `json:"startSeconds"`
	EndSeconds   float64   `json:"endSeconds"`
	Vector       []float32 `json:"vector"`
}

// AudioRepresentation contains only intended derived embeddings, never audio,
// PCM, spectrograms or intermediate hidden states. Vectors are L2-normalized.
type AudioRepresentation struct {
	ID                 string                       `json:"id"`
	TrackID            string                       `json:"trackId"`
	CatalogVersion     string                       `json:"catalogVersion"`
	TrackKey           string                       `json:"trackKey"`
	Identity           PreviewIdentity              `json:"identity"`
	AudioSHA256        string                       `json:"audioSha256"`
	Model              AudioRepresentationIdentity  `json:"model"`
	Segments           []AudioRepresentationSegment `json:"segments"`
	Pooled             []float32                    `json:"pooled"`
	Coverage           PreviewCoverage              `json:"coverage"`
	PreviewOffsetKnown bool                         `json:"previewOffsetKnown"`
	AnalyzedAt         string                       `json:"analyzedAt"`
}

// Bytes counts serialized representation payloads, not the shared database file.
type AudioRepresentationStorageUsage struct {
	Records int64 `json:"records"`
	Bytes   int64 `json:"bytes"`
}
