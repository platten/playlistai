package core

type Mode string

const (
	ModeSimilar Mode = "similar"
	ModeJourney Mode = "journey"
)

const (
	CurrentIntentVersion = 9
	DefaultCount         = 20
	DefaultCreativity    = 0.5
	DefaultNoise         = 0.0
	DefaultLookback      = 3
	MinCount             = 1
	MaxCount             = 100
	MinLookback          = 1
	MaxLookback          = 10
)

type ReferenceKind string
type Influence string

const (
	ReferenceArtist   ReferenceKind = "artist"
	ReferenceTrack    ReferenceKind = "track"
	ReferenceAlbum    ReferenceKind = "album"
	InfluencePositive Influence     = "positive"
	InfluenceNegative Influence     = "negative"
)

// SourceEvidence connects an interpretation to the user's words. Start/End are
// byte offsets when known; -1 means the text was preserved without offsets.
type SourceEvidence struct {
	Text     string `json:"text"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Explicit bool   `json:"explicit"`
}

type ResolutionStatus string

const (
	ResolutionResolved   ResolutionStatus = "resolved"
	ResolutionAmbiguous  ResolutionStatus = "ambiguous"
	ResolutionUnresolved ResolutionStatus = "unresolved"
)

// ResolutionEvidence explains why a catalog entity was proposed. Match is one
// of id, exact, alias, prefix, or tokens; NormalizedQuery records the form used
// without replacing the user's original query.
type ResolutionEvidence struct {
	Match           string `json:"match"`
	NormalizedQuery string `json:"normalizedQuery"`
	MatchedText     string `json:"matchedText"`
}

// WeightedTrack is a real catalog track used to represent an artist in vector
// retrieval. Weights sum to one for each resolved artist.
type WeightedTrack struct {
	TrackID string  `json:"trackId"`
	Weight  float64 `json:"weight"`
}

type ResolutionCandidate struct {
	Kind            ReferenceKind        `json:"kind"`
	EntityID        string               `json:"entityId"`
	Artist          string               `json:"artist"`
	Title           string               `json:"title"`
	Confidence      float64              `json:"confidence"`
	Evidence        []ResolutionEvidence `json:"evidence"`
	Representatives []WeightedTrack      `json:"representatives"`
}

type ReferenceResolution struct {
	Status         ResolutionStatus      `json:"status"`
	CatalogVersion string                `json:"catalogVersion"`
	Selected       *ResolutionCandidate  `json:"selected,omitempty"`
	Alternatives   []ResolutionCandidate `json:"alternatives"`
}

type IntentReference struct {
	Kind       ReferenceKind        `json:"kind"`
	Query      string               `json:"query"`
	TrackID    string               `json:"trackId"`
	Influence  Influence            `json:"influence"`
	Evidence   []SourceEvidence     `json:"evidence"`
	Resolution *ReferenceResolution `json:"resolution,omitempty"`
}

// MusicalCriterion is a defining part of the requested music. Unlike a soft
// preference it must be supported by affirmative musical evidence before a
// playlist can be reported as fulfilled. It is deliberately narrower than a
// hard constraint: a simple genre request is essential, while descriptive
// adjectives remain preferences unless the user makes them strict.
type MusicalCriterion struct {
	ConceptID string           `json:"conceptId,omitempty"`
	Strength  string           `json:"strength,omitempty"`
	Group     string           `json:"group,omitempty"`
	Kind      string           `json:"kind"` // style | mood | instrumentation | vocal
	Value     string           `json:"value"`
	Scope     string           `json:"scope"` // playlist | journey_start | journey_end | journey_via
	Evidence  []SourceEvidence `json:"evidence"`
}

type EvidenceState string

const (
	EvidenceMatch       EvidenceState = "match"
	EvidenceMismatch    EvidenceState = "mismatch"
	EvidenceUnknown     EvidenceState = "unknown"
	EvidenceUnsupported EvidenceState = "unsupported"
)

// AnchorSuitability is independent of entity-resolution confidence. A real
// catalog artist can resolve exactly while still being unsuitable for the
// musical criterion that caused the model to propose it.
type AnchorSuitability struct {
	State  EvidenceState       `json:"state"`
	Score  float64             `json:"score"` // evidence-native similarity, not a probability
	Detail string              `json:"detail"`
	Source []FeatureProvenance `json:"source"`
}

// InferredAnchor is a model-proposed retrieval aid, never a claim that the
// user named the entity. Role and Reason make complementary proposals
// inspectable; Reference holds catalog resolution separately from suitability.
type InferredAnchor struct {
	Reference   IntentReference   `json:"reference"`
	Role        string            `json:"role"`
	Reason      string            `json:"reason"`
	Suitability AnchorSuitability `json:"suitability"`
}

type IntentPreference struct {
	ConceptID string           `json:"conceptId,omitempty"`
	Scope     string           `json:"scope,omitempty"`
	Strength  string           `json:"strength,omitempty"`
	Degree    string           `json:"degree,omitempty"`
	Group     string           `json:"group,omitempty"`
	Value     string           `json:"value"`
	Influence Influence        `json:"influence"`
	Explicit  bool             `json:"explicit"`
	Evidence  []SourceEvidence `json:"evidence"`
}

type SemanticPreferences struct {
	// VocalPreferences preserves multiple stage-specific or alternative vocal
	// requests. VocalPreference remains the compatibility view for older data.
	VocalPreferences    []IntentPreference `json:"vocalPreferences,omitempty"`
	Genres              []IntentPreference `json:"genres"`
	Styles              []IntentPreference `json:"styles"`
	Moods               []IntentPreference `json:"moods"`
	Instrumentation     []IntentPreference `json:"instrumentation"`
	VocalPreference     *IntentPreference  `json:"vocalPreference,omitempty"`
	TextureDescriptions []IntentPreference `json:"textureDescriptions"`
}

// HardConstraint is explicit policy, not a wish. Supported=false is preserved
// and surfaced but never presented as enforced by the recommendation engine.
type HardConstraint struct {
	Kind            string           `json:"kind"`
	Value           string           `json:"value"`
	Supported       bool             `json:"supported"`
	RuntimeEnforced bool             `json:"runtimeEnforced"`
	Evidence        []SourceEvidence `json:"evidence"`
}

type UnsupportedRequirement struct {
	Text     string           `json:"text"`
	Reason   string           `json:"reason"`
	Evidence []SourceEvidence `json:"evidence"`
}

type IntentControls struct {
	RecommendationMode   RecommendationMode `json:"recommendationMode,omitempty"`
	TotalTrackCount      int                `json:"totalTrackCount"`
	AudioWeight          float64            `json:"audioWeight"`
	CooccurrenceWeight   float64            `json:"cooccurrenceWeight"`
	Discovery            float64            `json:"discovery"`
	ArtistDiversity      float64            `json:"artistDiversity"`
	TransitionSmoothness float64            `json:"transitionSmoothness"`
}

type EnergyPoint struct {
	Position float64 `json:"position"`
	Energy   float64 `json:"energy"`
}

type JourneyPlan struct {
	Waypoints        []IntentReference `json:"waypoints"`
	EnergyTrajectory []EnergyPoint     `json:"energyTrajectory"`
}

type CapabilityStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // supported | limited | unsupported
	Detail string `json:"detail"`
}

// IntentSeeds and IntentConstraints are retained as the v1/v2 storage and
// engine adapter. New code should author the typed v3 fields.
type IntentSeeds struct {
	Queries  []string `json:"queries"`
	TrackIDs []string `json:"trackIds"`
}

type IntentConstraints struct {
	ArtistsExclude           []string `json:"artistsExclude"`
	NoRepeatArtistBackToBack bool     `json:"noRepeatArtistBackToBack"`
	ExcludeSeedArtists       bool     `json:"excludeSeedArtists"`
}

type MusicIntent struct {
	Translation         *IntentTranslation       `json:"translation,omitempty"`
	Start               *IntentReference         `json:"start,omitempty"`
	DurationSeconds     int                      `json:"durationSeconds,omitempty"`
	VerificationPolicy  VerificationPolicy       `json:"verificationPolicy"`
	Temporal            []TemporalRequirement    `json:"temporal"`
	Destination         *IntentReference         `json:"destination,omitempty"`
	Knowledge           *KnowledgeSnapshot       `json:"knowledge,omitempty"`
	AnchorAttempts      []InferredAnchor         `json:"anchorAttempts"`
	OriginalDescription string                   `json:"originalDescription"`
	GenreExpansions     []GenreExpansion         `json:"genreExpansions"`
	Version             int                      `json:"version"`
	References          []IntentReference        `json:"references"`
	InferredAnchors     []InferredAnchor         `json:"inferredAnchors"`
	RequiredTracks      []IntentReference        `json:"requiredTracks"`
	EssentialCriteria   []MusicalCriterion       `json:"essentialCriteria"`
	Preferences         SemanticPreferences      `json:"preferences"`
	HardConstraints     []HardConstraint         `json:"hardConstraints"`
	Controls            IntentControls           `json:"controls"`
	Journey             JourneyPlan              `json:"journey"`
	Unsupported         []UnsupportedRequirement `json:"unsupportedRequirements"`
	Capabilities        []CapabilityStatus       `json:"capabilities"`
	InterpretationNotes string                   `json:"interpretationNotes"`

	// Deprecated v1/v2 fields. Normalized migrates from and backfills these so
	// existing history and the current recommendation engine remain compatible.
	Seeds        IntentSeeds       `json:"seeds,omitempty"`
	Required     IntentSeeds       `json:"required,omitempty"`
	Count        int               `json:"count,omitempty"`
	Mode         Mode              `json:"mode"`
	Creativity   float64           `json:"creativity,omitempty"`
	Noise        float64           `json:"noise,omitempty"`
	Lookback     int               `json:"lookback,omitempty"`
	Constraints  IntentConstraints `json:"constraints,omitempty"`
	Seed         RNGSeed           `json:"seed"`
	NotesForUser string            `json:"notesForUser"`
}
