package core

// CurrentFeatureSchemaVersion versions the semantic sidecar contract. Readers
// explicitly decide which older versions remain compatible.
const CurrentFeatureSchemaVersion = 2

type FeatureMissingness string

const (
	FeatureKnown   FeatureMissingness = "known"
	FeatureUnknown FeatureMissingness = "unknown"
)

// FeatureProvenance identifies the evidence behind one feature. SourceID is an
// entity or document identifier, not a URL guessed by the application.
type FeatureProvenance struct {
	Source        string  `json:"source"`
	SourceID      string  `json:"sourceId"`
	SourceVersion string  `json:"sourceVersion"`
	ModelVersion  string  `json:"modelVersion"`
	Confidence    float64 `json:"confidence"`
}

type FeatureValue struct {
	Value       string              `json:"value"`
	Missingness FeatureMissingness  `json:"missingness"`
	Confidence  float64             `json:"confidence"`
	Provenance  []FeatureProvenance `json:"provenance"`
}

type ReleaseDateFeatures struct {
	OriginalEdition FeatureValue `json:"originalEdition"`
	ReleaseEdition  FeatureValue `json:"releaseEdition"`
}

type PreviewCoverage struct {
	Available      bool    `json:"available"`
	StartSeconds   float64 `json:"startSeconds"`
	EndSeconds     float64 `json:"endSeconds"`
	CoveredSeconds float64 `json:"coveredSeconds"`
	Source         string  `json:"source"`
}

// TrackFeatures contains only supplied evidence. Empty facets must remain
// unknown; consumers must never synthesize labels from artist or title text.
type TrackFeatures struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	CatalogVersion  string              `json:"catalogVersion"`
	TrackID         string              `json:"trackId"`
	ArtistID        FeatureValue        `json:"artistId"`
	RecordingID     FeatureValue        `json:"recordingId"`
	Tags            []FeatureValue      `json:"tags"`
	Descriptions    []FeatureValue      `json:"descriptions"`
	Styles          []FeatureValue      `json:"styles"`
	Moods           []FeatureValue      `json:"moods"`
	Instrumentation []FeatureValue      `json:"instrumentation"`
	VocalEvidence   FeatureValue        `json:"vocalEvidence"` // vocal | instrumental | mixed | unknown
	ReleaseDates    ReleaseDateFeatures `json:"releaseDates"`
	Preview         PreviewCoverage     `json:"previewCoverage"`
}

type FeatureStoreInfo struct {
	SchemaVersion   int      `json:"schemaVersion"`
	CatalogVersion  string   `json:"catalogVersion"`
	FeatureVersion  string   `json:"featureVersion"`
	TextModel       string   `json:"textModel"`
	ModelRevision   string   `json:"modelRevision"`
	EmbeddingDim    int      `json:"embeddingDim"`
	QueryEncoder    string   `json:"queryEncoder"`
	QueryTermCount  int      `json:"queryTermCount"`
	TrackCount      int      `json:"trackCount"`
	SupportedFacets []string `json:"supportedFacets"`
}

type SemanticHit struct {
	TrackID string  `json:"trackId"`
	Score   float64 `json:"score"` // cosine similarity, not a probability
}
