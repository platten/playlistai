package core

// AutomaticFitPolicyVersion identifies an estimated agreement policy. Its
// categories are not calibrated probabilities or factual verification labels.
const AutomaticFitPolicyVersion = "automatic-fit/v5"

// AutomaticEstimatedClause separates musical character from explicit must-haves.
// Empty legacy strengths are never reinterpreted as permission to relax a gate.
func AutomaticEstimatedClause(c AudioClause) bool {
	if c.Strict || c.Negative || (c.Strength != "preferred" && c.Strength != "essential") {
		return false
	}
	return c.Kind == "mood" || c.Kind == "instrumentation" || c.Kind == "texture" || c.Kind == "description"
}

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
	Score            float64                     `json:"score,omitempty"`
	ScoreAvailable   bool                        `json:"scoreAvailable,omitempty"`
	ModelFingerprint string                      `json:"modelFingerprint,omitempty"`
	Calibration      *AudioSimilarityCalibration `json:"calibration,omitempty"`
	Family           string                      `json:"family"`
	State            EvidenceState               `json:"state"`
	Detail           string                      `json:"detail"`
	Coverage         *PreviewCoverage            `json:"coverage,omitempty"`
	LibraryCoverage  *LibraryCLAPCoverage        `json:"libraryCoverage,omitempty"`
}

type AutomaticClauseFit struct {
	EstimateScore     float64              `json:"estimateScore,omitempty"`
	EstimateAvailable bool                 `json:"estimateAvailable,omitempty"`
	Clause            AudioClause          `json:"clause"`
	State             AutomaticFitState    `json:"state"`
	Signals           []AutomaticFitSignal `json:"signals,omitempty"`
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
