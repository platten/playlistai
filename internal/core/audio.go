package core

// AudioModelIdentity pins the complete aligned embedding space. Recommendation
// catalog vectors are never compatible with these audio/text embeddings.
type AudioModelIdentity struct {
	Model         string `json:"model"`
	Revision      string `json:"revision"`
	Preprocessing string `json:"preprocessing"`
	Runtime       string `json:"runtime"`
	Dimension     int    `json:"dimension"`
	Weights       string `json:"weights,omitempty"` // fingerprint of paired encoders and tokenizer
}

type PreviewIdentity struct {
	Status       ResolutionStatus `json:"status"`
	Provider     string           `json:"provider"`
	ProviderID   string           `json:"providerId"`
	RecordingID  string           `json:"recordingId"`
	ISRC         string           `json:"isrc"`
	Artist       string           `json:"artist"`
	Title        string           `json:"title"`
	Method       string           `json:"method"`
	Alternatives []string         `json:"alternatives"`
}

// URL is transient: signed CDN links and audio payloads are not analysis data.
type ResolvedAudioPreview struct {
	Identity PreviewIdentity `json:"identity"`
	URL      string          `json:"-"`
}

type AudioSegment struct {
	StartSeconds float64   `json:"startSeconds"`
	EndSeconds   float64   `json:"endSeconds"`
	Embedding    []float32 `json:"embedding"`
}

// AudioSampling records how a reusable evidence interval was selected. The
// interval itself is in Coverage, relative to the provider preview unless its
// offset into the recording is known. Nil denotes a legacy whole-preview row.
type AudioSampling struct {
	Policy           string  `json:"policy"`
	AvailableSeconds float64 `json:"availableSeconds"`
}

type AudioAnalysis struct {
	ID             string             `json:"id"`
	TrackID        string             `json:"trackId"`
	CatalogVersion string             `json:"catalogVersion"`
	TrackKey       string             `json:"trackKey"`
	Model          AudioModelIdentity `json:"model"`
	Identity       PreviewIdentity    `json:"identity"`
	AudioSHA256    string             `json:"audioSha256"`
	Segments       []AudioSegment     `json:"segments"`
	Coverage       PreviewCoverage    `json:"coverage"`
	Sampling       *AudioSampling     `json:"sampling,omitempty"`
	// PreviewOffsetKnown=false means segment times are relative to the preview,
	// not offsets into the complete recording.
	PreviewOffsetKnown bool   `json:"previewOffsetKnown"`
	AnalyzedAt         string `json:"analyzedAt"`
}

type AudioClause struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Scope     string `json:"scope"`
	Negative  bool   `json:"negative"`
	Essential bool   `json:"essential"`
	Strict    bool   `json:"strict"`
}

type AudioClauseAssessment struct {
	Clause AudioClause `json:"clause"`
	Score  float64     `json:"score"` // cosine similarity, never a probability
	// A usable similarity can guide ranking while categorical fit stays unknown.
	ScoreAvailable bool          `json:"scoreAvailable,omitempty"`
	State          EvidenceState `json:"state"`
}

type AudioAssessment struct {
	TrackID           string                  `json:"trackId"`
	AnalysisID        string                  `json:"analysisId"`
	Identity          PreviewIdentity         `json:"identity"`
	IntentFingerprint string                  `json:"intentFingerprint"`
	PolicyVersion     string                  `json:"policyVersion"`
	Eligible          bool                    `json:"eligible"`
	Clauses           []AudioClauseAssessment `json:"clauses"`
	Detail            string                  `json:"detail"`
}

type AudioEvidenceSnapshot struct {
	ID                  string             `json:"id"`
	Model               AudioModelIdentity `json:"model"`
	PolicyVersion       string             `json:"policyVersion"`
	Assessments         []AudioAssessment  `json:"assessments"`
	NewAnalyses         int                `json:"newAnalyses"`
	CacheHits           int                `json:"cacheHits"`
	BytesFetched        int64              `json:"bytesFetched"`
	ElapsedMilliseconds int64              `json:"elapsedMilliseconds"`
	Stopped             bool               `json:"stopped"`
	BudgetExhausted     bool               `json:"budgetExhausted"`
}

type AnalysisStorageUsage struct {
	Records     int64 `json:"records"`
	Assessments int64 `json:"assessments"`
	Bytes       int64 `json:"bytes"`
}

// GenreExpansion is model interpretation for retrieval only. The user's
// original genre remains an essential criterion even when it is unfamiliar.
type GenreExpansion struct {
	Genre           string   `json:"genre"`
	Characteristics string   `json:"characteristics"`
	RelatedGenres   []string `json:"relatedGenres"`
}
