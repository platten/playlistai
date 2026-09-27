package core

// RecordingEvidencePolicyVersion identifies claim scope and reconciliation
// rules independently of provider caches and audio similarity policies.
const RecordingEvidencePolicyVersion = "recording-evidence/v5"

// RecordingClaim is a source-attributed fact, not a model's opinion about a
// request. Scope and entity identity prevent artist or album descriptions from
// becoming facts about every recording. State describes support for Value.
type RecordingClaim struct {
	Kind             string           `json:"kind"`
	Value            string           `json:"value"`
	State            EvidenceState    `json:"state"`
	Scope            string           `json:"scope"`
	EntityID         string           `json:"entityId"`
	RecordingID      string           `json:"recordingId,omitempty"`
	ArtistID         string           `json:"artistId,omitempty"`
	Source           ContextSource    `json:"source"`
	Locator          string           `json:"locator"`
	Method           string           `json:"method"`
	RetrievedAt      string           `json:"retrievedAt,omitempty"`
	ExtractorVersion string           `json:"extractorVersion,omitempty"`
	Coverage         *PreviewCoverage `json:"coverage,omitempty"`
}

// CriterionAssessment retains disagreement and missing evidence. A partial
// audio observation never acquires whole-recording coverage through this type.
type CriterionAssessment struct {
	Clause   AudioClause      `json:"clause"`
	State    EvidenceState    `json:"state"`
	Conflict bool             `json:"conflict,omitempty"`
	Claims   []RecordingClaim `json:"claims,omitempty"`
	Detail   string           `json:"detail,omitempty"`
}
