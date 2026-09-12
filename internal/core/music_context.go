package core

// ContextProfileVersion changes when source interpretation or contextual seed
// selection changes. Context is a retrieval aid, never recording evidence.
const ContextProfileVersion = "music-context/v1"

// ContextSource identifies reusable public context independently of the user's
// SourceEvidence. Revision is an immutable source revision or content digest.
type ContextSource struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	License  string `json:"license"`
}

// ContextProfile describes an artist, album or genre. Description and Genres
// are source-attributed retrieval hints; they cannot satisfy a track criterion.
// FirstYear/LastYear and ReleaseGroupIDs describe the proposed seed scope, not
// an inferred hard restriction on the output playlist.
type ContextProfile struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	EntityKey        string          `json:"entityKey"`
	Query            string          `json:"query"`
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	Genres           []string        `json:"genres,omitempty"`
	Characteristics  []string        `json:"characteristics,omitempty"`
	Sources          []ContextSource `json:"sources,omitempty"`
	ReleaseGroupIDs  []string        `json:"releaseGroupIds,omitempty"`
	FirstYear        int             `json:"firstYear,omitempty"`
	LastYear         int             `json:"lastYear,omitempty"`
	ScopeNote        string          `json:"scopeNote,omitempty"`
	ExtractorVersion string          `json:"extractorVersion"`
}

// ContextSeedPlan keeps contextual representatives separate from the requested
// reference and from required output tracks. EntityKey is the original catalog
// resolution key; Profile.EntityKey is the linked external identity. Empty
// EntityKey falls back to ReferenceKind+Query. Scope uses the intent scopes
// playlist, journey_start, journey_end and journey_via.
type ContextSeedPlan struct {
	ReferenceKind ReferenceKind   `json:"referenceKind"`
	Query         string          `json:"query"`
	EntityKey     string          `json:"entityKey"`
	Scope         string          `json:"scope"`
	Seeds         []WeightedTrack `json:"seeds,omitempty"`
	Profile       ContextProfile  `json:"profile"`
}
