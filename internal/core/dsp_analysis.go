package core

// DSPAnalysis stores only measurements of an observed preview interval. Version
// pins extraction, original decoding and interval selection independently of
// CLAP, MERT and recommendation policy versions.
type DSPAnalysis struct {
	ID                 string          `json:"id"`
	TrackID            string          `json:"trackId"`
	CatalogVersion     string          `json:"catalogVersion"`
	TrackKey           string          `json:"trackKey"`
	Identity           PreviewIdentity `json:"identity"`
	AudioSHA256        string          `json:"audioSha256"`
	Version            string          `json:"version"`
	SampleRate         int             `json:"sampleRate"`
	Channels           int             `json:"channels"`
	Features           DSPFeatures     `json:"features"`
	Coverage           PreviewCoverage `json:"coverage"`
	PreviewOffsetKnown bool            `json:"previewOffsetKnown"`
	AnalyzedAt         string          `json:"analyzedAt"`
}

// Bytes is the serialized payload size, not the shared SQLite file size.
type DSPStorageUsage struct {
	Records int64 `json:"records"`
	Bytes   int64 `json:"bytes"`
}
