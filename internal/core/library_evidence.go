package core

// LibraryEvidenceSource identifies a measured observation, not a probability or
// a claim about an entire recording. SpaceID hashes the complete representation
// contract; matching dimensionality alone never permits comparison.
type LibraryEvidenceSource struct {
	PackID     string `json:"packId"`
	SpaceID    string `json:"spaceId,omitempty"`
	Generation string `json:"generation"`
	Scope      string `json:"scope"`
}

type LibraryVector struct {
	Source LibraryEvidenceSource `json:"source"`
	Values []float32             `json:"values"`
}

// AudioClauseVector is a request-local mean of compatible text embeddings.
// It is deliberately not normalized: dot products retain the mean cosine of
// the fixed caption ensemble. Neither its values nor its scores are probabilities.
type AudioClauseVector struct {
	Clause AudioClause
	Values []float32
}

// LibraryCLAPCoverage records only observed excerpts, never padded encoder
// input or a claim that the complete recording was analyzed.
type LibraryCLAPCoverage struct {
	CoveredSeconds float64                `json:"coveredSeconds"`
	Incomplete     bool                   `json:"incomplete"`
	PartialReason  string                 `json:"partialReason,omitempty"`
	Segments       []LibraryAudioInterval `json:"segments"`
}

type LibraryAudioInterval struct {
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
}

// LibraryTrackFeatures are validated recording-level tags. Pointers preserve
// unknown versus zero; descriptor values retain their declared source scale.
type LibraryTrackFeatures struct {
	Conflicts       map[string]bool
	TempoBPM        *float64
	Key             string
	OriginalYear    *int
	CompositionYear *int
	Descriptors     map[string]float64
}

// LibraryTaste stores explicit preferences in one independent audio space.
// Library membership itself never adds a positive observation.
type LibraryTaste struct {
	Source           LibraryEvidenceSource `json:"source"`
	Positive         []float32             `json:"positive"`
	Negative         []float32             `json:"negative"`
	RequestPositive  []float32             `json:"requestPositive"`
	RequestNegative  []float32             `json:"requestNegative"`
	Clusters         [][]float32           `json:"clusters"`
	EvidenceTrackIDs []string              `json:"evidenceTrackIds"`
}
