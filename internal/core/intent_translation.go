package core

// IntentTranslation records the source-grounded interpretation used by this
// request. It is persisted as evidence; loading history never reruns extraction.
type IntentTranslation struct {
	Version   string           `json:"version"`
	Atoms     []IntentAtom     `json:"atoms"`
	Repairs   []string         `json:"repairs,omitempty"`
	Proposals []IntentProposal `json:"proposals,omitempty"`
}

// IntentProposal is advisory provenance, never an independently locked fact.
// Score is an uncalibrated model score, not evidence about a recording.
type IntentProposal struct {
	Origin    string         `json:"origin,omitempty"`
	Role      string         `json:"role,omitempty"`
	Kind      string         `json:"kind"`
	Value     string         `json:"value"`
	ConceptID string         `json:"conceptId,omitempty"`
	Source    SourceEvidence `json:"source"`
	Model     string         `json:"model"`
	Score     float64        `json:"score"`
	Advisory  bool           `json:"advisory"`
}

// IntentAtom retains occurrence-specific intent. Evidence offsets are UTF-8
// byte offsets into OriginalDescription. Strength is essential, preferred or
// required; Group identifies alternatives, never an implicit conjunction.
type IntentAtom struct {
	ID        string           `json:"id"`
	ConceptID string           `json:"conceptId,omitempty"`
	Kind      string           `json:"kind"`
	Value     string           `json:"value"`
	Scope     string           `json:"scope"`
	Polarity  string           `json:"polarity"`
	Strength  string           `json:"strength"`
	Degree    string           `json:"degree,omitempty"`
	Group     string           `json:"group,omitempty"`
	Evidence  []SourceEvidence `json:"evidence"`
}

func cloneTranslation(in *IntentTranslation) *IntentTranslation {
	if in == nil {
		return nil
	}
	out := *in
	out.Repairs = append([]string(nil), in.Repairs...)
	out.Proposals = append([]IntentProposal(nil), in.Proposals...)
	out.Atoms = append([]IntentAtom(nil), in.Atoms...)
	for i := range out.Atoms {
		out.Atoms[i].Evidence = append([]SourceEvidence(nil), in.Atoms[i].Evidence...)
	}
	return &out
}
