package core

// IntentTranslation records the source-grounded interpretation used by this
// request. It is persisted as evidence; loading history never reruns extraction.
type IntentTranslation struct {
	Version      string            `json:"version"`
	OriginalText string            `json:"originalText,omitempty"`
	Quoted       []SourceRegion    `json:"quotedRegions,omitempty"`
	Markers      []SyntacticMarker `json:"syntacticMarkers,omitempty"`
	Atoms        []IntentAtom      `json:"atoms"`
	Recognition  RecognitionStatus `json:"recognition,omitempty"`
	Repairs      []string          `json:"repairs,omitempty"`
}

type SourceRegion struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type SyntacticMarker struct {
	Kind  string `json:"kind"` // exclusion | inclusion | alternative | journey_start | journey_via | journey_end
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// RecognitionStatus describes which offline resources were available for a
// request-local recognition pass. Missing fields in saved intents mean that
// grounding was unavailable when the intent was created.
type RecognitionStatus struct {
	MatcherVersion      string   `json:"matcherVersion,omitempty"`
	ReferenceLookup     string   `json:"referenceLookup,omitempty"` // available | unavailable | incomplete
	ReferenceSnapshot   string   `json:"referenceSnapshot,omitempty"`
	GenreVocabulary     string   `json:"genreVocabulary,omitempty"` // installed | embedded
	GenreVocabularyHash string   `json:"genreVocabularyHash,omitempty"`
	Incomplete          bool     `json:"incomplete,omitempty"`
	Notices             []string `json:"notices,omitempty"`
}

// IntentAtom retains occurrence-specific intent. Evidence offsets are UTF-8
// byte offsets into OriginalDescription. Strength is essential, preferred or
// required; Group identifies alternatives, never an implicit conjunction.
type IntentAtom struct {
	ID        string             `json:"id"`
	ConceptID string             `json:"conceptId,omitempty"`
	Kind      string             `json:"kind"`
	Value     string             `json:"value"`
	Scope     string             `json:"scope"`
	Polarity  string             `json:"polarity"`
	Strength  string             `json:"strength"`
	Degree    string             `json:"degree,omitempty"`
	Group     string             `json:"group,omitempty"`
	Evidence  []SourceEvidence   `json:"evidence"`
	Grounding *IdentityGrounding `json:"grounding,omitempty"`
}

func cloneTranslation(in *IntentTranslation) *IntentTranslation {
	if in == nil {
		return nil
	}
	out := *in
	out.Repairs = append([]string(nil), in.Repairs...)
	out.Quoted = append([]SourceRegion(nil), in.Quoted...)
	out.Markers = append([]SyntacticMarker(nil), in.Markers...)
	out.Recognition.Notices = append([]string(nil), in.Recognition.Notices...)
	out.Atoms = append([]IntentAtom(nil), in.Atoms...)
	for i := range out.Atoms {
		out.Atoms[i].Evidence = append([]SourceEvidence(nil), in.Atoms[i].Evidence...)
		out.Atoms[i].Grounding = cloneIdentityGrounding(in.Atoms[i].Grounding)
	}
	return &out
}

func cloneIdentityGrounding(in *IdentityGrounding) *IdentityGrounding {
	if in == nil {
		return nil
	}
	out := *in
	out.Candidates = append([]IdentityCandidate(nil), in.Candidates...)
	return &out
}
