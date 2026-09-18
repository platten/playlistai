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
