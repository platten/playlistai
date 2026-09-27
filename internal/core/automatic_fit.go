package core

// AutomaticFitPolicyVersion identifies an estimated agreement policy. Its
// categories are not calibrated probabilities or factual verification labels.
const AutomaticFitPolicyVersion = "automatic-fit/v4"

type AutomaticFitState string

const (
	AutomaticStrong      AutomaticFitState = "strong"
	AutomaticPlausible   AutomaticFitState = "plausible"
	AutomaticUnknown     AutomaticFitState = "unknown"
	AutomaticConflicting AutomaticFitState = "conflicting"
)

// AutomaticFitSignal retains the family used for deduplication. Several tags
// copied from one classifier never become independent agreement.
type AutomaticFitSignal struct {
	Calibration     *AudioSimilarityCalibration `json:"calibration,omitempty"`
	Family          string                      `json:"family"`
	State           EvidenceState               `json:"state"`
	Detail          string                      `json:"detail"`
	Coverage        *PreviewCoverage            `json:"coverage,omitempty"`
	LibraryCoverage *LibraryCLAPCoverage        `json:"libraryCoverage,omitempty"`
}

type AutomaticClauseFit struct {
	Clause  AudioClause          `json:"clause"`
	State   AutomaticFitState    `json:"state"`
	Signals []AutomaticFitSignal `json:"signals,omitempty"`
}

type AutomaticFitAssessment struct {
	PolicyVersion string               `json:"policyVersion"`
	State         AutomaticFitState    `json:"state"`
	Clauses       []AutomaticClauseFit `json:"clauses,omitempty"`
	Detail        string               `json:"detail"`
}

type AutomaticTrackFit struct {
	TrackID    string                 `json:"trackId"`
	Assessment AutomaticFitAssessment `json:"assessment"`
}
