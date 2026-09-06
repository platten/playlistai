package core

import (
	"fmt"
	"math"
	"strings"
)

type Mode string

const (
	ModeSimilar Mode = "similar"
	ModeJourney Mode = "journey"
)

const (
	CurrentIntentVersion = 5
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

type IntentPreference struct {
	Value     string           `json:"value"`
	Influence Influence        `json:"influence"`
	Explicit  bool             `json:"explicit"`
	Evidence  []SourceEvidence `json:"evidence"`
}

type SemanticPreferences struct {
	Styles              []IntentPreference `json:"styles"`
	Moods               []IntentPreference `json:"moods"`
	Instrumentation     []IntentPreference `json:"instrumentation"`
	VocalPreference     *IntentPreference  `json:"vocalPreference,omitempty"`
	TextureDescriptions []IntentPreference `json:"textureDescriptions"`
}

// HardConstraint is explicit policy, not a wish. Supported=false is preserved
// and surfaced but never presented as enforced by the recommendation engine.
type HardConstraint struct {
	Kind      string           `json:"kind"`
	Value     string           `json:"value"`
	Supported bool             `json:"supported"`
	Evidence  []SourceEvidence `json:"evidence"`
}

type UnsupportedRequirement struct {
	Text     string           `json:"text"`
	Reason   string           `json:"reason"`
	Evidence []SourceEvidence `json:"evidence"`
}

type IntentControls struct {
	TotalTrackCount      int     `json:"totalTrackCount"`
	AudioWeight          float64 `json:"audioWeight"`
	CooccurrenceWeight   float64 `json:"cooccurrenceWeight"`
	Discovery            float64 `json:"discovery"`
	ArtistDiversity      float64 `json:"artistDiversity"`
	TransitionSmoothness float64 `json:"transitionSmoothness"`
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
	Version             int                      `json:"version"`
	References          []IntentReference        `json:"references"`
	RequiredTracks      []IntentReference        `json:"requiredTracks"`
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

func (m MusicIntent) Normalized() MusicIntent {
	out := m
	legacyOnly := len(out.References) == 0 && len(out.RequiredTracks) == 0 &&
		(!seedSetEmpty(out.Seeds) || !seedSetEmpty(out.Required))
	// Only v1/v2 need semantic migration. V3 already has the typed intent;
	// v4 adds resolution metadata and v5 makes RNG seeds lossless strings.
	if out.Version < 3 || legacyOnly {
		out = migrateLegacy(out)
	}
	out.Version = CurrentIntentVersion
	out.References = cleanReferences(out.References, false)
	out.RequiredTracks = cleanReferences(out.RequiredTracks, true)
	out.Journey.Waypoints = cleanReferences(out.Journey.Waypoints, false)
	out.Preferences = cleanPreferences(out.Preferences)
	out.HardConstraints = cleanHardConstraints(out.HardConstraints)
	out.Unsupported = cleanUnsupported(out.Unsupported)
	out.Unsupported = addUnsupportedConstraints(out.Unsupported, out.HardConstraints)

	out.Controls.TotalTrackCount = clampInt(orDefaultInt(out.Controls.TotalTrackCount, DefaultCount), MinCount, MaxCount)
	if out.Controls.AudioWeight == 0 && out.Controls.CooccurrenceWeight == 0 {
		out.Controls.AudioWeight, out.Controls.CooccurrenceWeight = 0.5, 0.5
	}
	out.Controls.AudioWeight = clampFloat(out.Controls.AudioWeight, 0, 1)
	out.Controls.CooccurrenceWeight = clampFloat(out.Controls.CooccurrenceWeight, 0, 1)
	out.Controls.Discovery = clampFloat(out.Controls.Discovery, 0, 1)
	out.Controls.ArtistDiversity = clampFloat(out.Controls.ArtistDiversity, 0, 1)
	out.Controls.TransitionSmoothness = clampFloat(out.Controls.TransitionSmoothness, 0, 1)
	if seed, err := out.Seed.Canonical(); err == nil {
		out.Seed = seed
	}

	if out.Mode != ModeSimilar && out.Mode != ModeJourney {
		if len(out.Journey.Waypoints) >= 2 || positiveReferenceCount(out.References) >= 2 {
			out.Mode = ModeJourney
		} else {
			out.Mode = ModeSimilar
		}
	}
	out.Capabilities = intentCapabilities()
	out.backfillEngineAdapter()
	return out
}

// Validate checks semantic invariants that JSON decoding and GBNF cannot.
func (m MusicIntent) Validate() error {
	if _, err := m.Seed.Canonical(); err != nil {
		return fmt.Errorf("intent: %w", err)
	}
	if m.Mode != "" && m.Mode != ModeSimilar && m.Mode != ModeJourney {
		return fmt.Errorf("intent: invalid mode %q", m.Mode)
	}
	for _, group := range [][]IntentReference{m.References, m.RequiredTracks, m.Journey.Waypoints} {
		for _, ref := range group {
			if ref.Kind != ReferenceArtist && ref.Kind != ReferenceTrack {
				return fmt.Errorf("intent: invalid reference kind %q", ref.Kind)
			}
			if ref.Influence != InfluencePositive && ref.Influence != InfluenceNegative {
				return fmt.Errorf("intent: invalid influence %q", ref.Influence)
			}
			if strings.TrimSpace(ref.Query) == "" && strings.TrimSpace(ref.TrackID) == "" {
				return fmt.Errorf("intent: reference has no query or track id")
			}
			if ref.Resolution != nil {
				if err := validateResolution(ref.Kind, *ref.Resolution); err != nil {
					return err
				}
			}
		}
	}
	for _, ref := range m.RequiredTracks {
		if ref.Kind != ReferenceTrack || ref.Influence != InfluencePositive {
			return fmt.Errorf("intent: required tracks must be positive track references")
		}
	}
	preferenceGroups := [][]IntentPreference{
		m.Preferences.Styles, m.Preferences.Moods,
		m.Preferences.Instrumentation, m.Preferences.TextureDescriptions,
	}
	for _, group := range preferenceGroups {
		for _, preference := range group {
			if strings.TrimSpace(preference.Value) == "" ||
				(preference.Influence != InfluencePositive && preference.Influence != InfluenceNegative) {
				return fmt.Errorf("intent: invalid semantic preference")
			}
		}
	}
	if vocal := m.Preferences.VocalPreference; vocal != nil &&
		(strings.TrimSpace(vocal.Value) == "" ||
			(vocal.Influence != InfluencePositive && vocal.Influence != InfluenceNegative)) {
		return fmt.Errorf("intent: invalid vocal preference")
	}
	for _, constraint := range m.HardConstraints {
		if strings.TrimSpace(constraint.Kind) == "" || strings.TrimSpace(constraint.Value) == "" {
			return fmt.Errorf("intent: hard constraint must have kind and value")
		}
		if constraint.Supported != HardConstraintSupported(constraint.Kind) {
			return fmt.Errorf("intent: incorrect capability claim for hard constraint %q", constraint.Kind)
		}
	}
	c := m.Controls
	for name, value := range map[string]float64{
		"audio weight": c.AudioWeight, "cooccurrence weight": c.CooccurrenceWeight,
		"discovery": c.Discovery, "artist diversity": c.ArtistDiversity,
		"transition smoothness": c.TransitionSmoothness,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("intent: %s must be between 0 and 1", name)
		}
	}
	if c.TotalTrackCount != 0 && (c.TotalTrackCount < MinCount || c.TotalTrackCount > MaxCount) {
		return fmt.Errorf("intent: total track count must be between %d and %d", MinCount, MaxCount)
	}
	lastPosition := -1.0
	for _, point := range m.Journey.EnergyTrajectory {
		if point.Position < 0 || point.Position > 1 || point.Energy < 0 || point.Energy > 1 {
			return fmt.Errorf("intent: energy trajectory values must be between 0 and 1")
		}
		if point.Position < lastPosition {
			return fmt.Errorf("intent: energy trajectory positions must be ordered")
		}
		lastPosition = point.Position
	}
	return nil
}

func migrateLegacy(m MusicIntent) MusicIntent {
	if m.Version < 2 && seedSetEmpty(m.Required) {
		m.Required = m.Seeds
	}
	for _, q := range m.Seeds.Queries {
		m.References = append(m.References, legacyReference(ReferenceArtist, q, ""))
	}
	for _, id := range m.Seeds.TrackIDs {
		m.References = append(m.References, legacyReference(ReferenceTrack, "", id))
	}
	for _, q := range m.Required.Queries {
		m.RequiredTracks = append(m.RequiredTracks, legacyReference(ReferenceTrack, q, ""))
	}
	for _, id := range m.Required.TrackIDs {
		m.RequiredTracks = append(m.RequiredTracks, legacyReference(ReferenceTrack, "", id))
	}
	for _, artist := range m.Constraints.ArtistsExclude {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "exclude_artist", Value: artist, Supported: true})
	}
	if m.Constraints.ExcludeSeedArtists {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "exclude_reference_artists", Value: "true", Supported: true})
	}
	if m.Constraints.NoRepeatArtistBackToBack {
		m.HardConstraints = append(m.HardConstraints, HardConstraint{Kind: "no_back_to_back_artist", Value: "true", Supported: true})
	}
	m.Controls = IntentControls{
		TotalTrackCount:      m.Count,
		AudioWeight:          m.Creativity,
		CooccurrenceWeight:   1 - m.Creativity,
		Discovery:            m.Noise,
		TransitionSmoothness: float64(clampInt(orDefaultInt(m.Lookback, DefaultLookback), MinLookback, MaxLookback)-1) / 9,
	}
	return m
}

func legacyReference(kind ReferenceKind, query, id string) IntentReference {
	return IntentReference{Kind: kind, Query: query, TrackID: id, Influence: InfluencePositive}
}

func (m *MusicIntent) backfillEngineAdapter() {
	m.Seeds = IntentSeeds{}
	allReferences := append(append([]IntentReference(nil), m.References...), m.Journey.Waypoints...)
	for _, ref := range allReferences {
		if ref.Influence != InfluencePositive {
			continue
		}
		if ref.TrackID != "" {
			m.Seeds.TrackIDs = appendUnique(m.Seeds.TrackIDs, ref.TrackID)
		} else {
			m.Seeds.Queries = appendUnique(m.Seeds.Queries, ref.Query)
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, representative := range ref.Resolution.Selected.Representatives {
				m.Seeds.TrackIDs = appendUnique(m.Seeds.TrackIDs, representative.TrackID)
			}
		}
	}
	m.Required = IntentSeeds{}
	for _, ref := range m.RequiredTracks {
		if ref.TrackID != "" {
			m.Required.TrackIDs = appendUnique(m.Required.TrackIDs, ref.TrackID)
		} else {
			m.Required.Queries = appendUnique(m.Required.Queries, ref.Query)
		}
	}
	m.Count = m.Controls.TotalTrackCount
	weightTotal := m.Controls.AudioWeight + m.Controls.CooccurrenceWeight
	m.Creativity = 0.5
	if weightTotal > 0 {
		m.Creativity = m.Controls.AudioWeight / weightTotal
	}
	m.Noise = m.Controls.Discovery
	m.Lookback = 1 + int(math.Round(m.Controls.TransitionSmoothness*9))
	m.Constraints = IntentConstraints{}
	for _, constraint := range m.HardConstraints {
		if !constraint.Supported {
			continue
		}
		switch constraint.Kind {
		case "exclude_artist":
			m.Constraints.ArtistsExclude = appendUnique(m.Constraints.ArtistsExclude, constraint.Value)
		case "exclude_reference_artists":
			m.Constraints.ExcludeSeedArtists = true
		case "no_back_to_back_artist":
			m.Constraints.NoRepeatArtistBackToBack = true
		}
	}
}

func intentCapabilities() []CapabilityStatus {
	return []CapabilityStatus{
		{Name: "positive_references", Status: "supported", Detail: "catalog embedding similarity"},
		{Name: "reference_resolution", Status: "supported", Detail: "typed exact, alias, and ranked catalog matching with explicit ambiguity"},
		{Name: "negative_references", Status: "limited", Detail: "only explicit hard artist exclusions are enforceable"},
		{Name: "required_tracks", Status: "supported", Detail: "resolved catalog tracks"},
		{Name: "hard_artist_exclusions", Status: "supported", Detail: "exact normalized artist identity"},
		{Name: "audio_cooccurrence_weights", Status: "supported", Detail: "independent weights are normalized for blended similarity"},
		{Name: "total_track_count", Status: "supported", Detail: "total output length including required tracks"},
		{Name: "semantic_preferences", Status: "unsupported", Detail: "preserved but catalog has no semantic attributes"},
		{Name: "discovery", Status: "supported", Detail: "seeded vector-search variation"},
		{Name: "artist_diversity", Status: "limited", Detail: "only explicit back-to-back artist prevention is enforceable"},
		{Name: "transition_smoothness", Status: "limited", Detail: "controls walk lookback and journey interpolation"},
		{Name: "energy_trajectory", Status: "unsupported", Detail: "preserved but catalog has no energy feature"},
	}
}

func cleanReferences(in []IntentReference, required bool) []IntentReference {
	var out []IntentReference
	seen := map[string]struct{}{}
	for _, ref := range in {
		ref.Query = strings.TrimSpace(ref.Query)
		ref.TrackID = strings.TrimSpace(ref.TrackID)
		ref.Resolution = cleanResolution(ref.Resolution)
		if ref.Kind == "" {
			if ref.TrackID != "" || required {
				ref.Kind = ReferenceTrack
			} else {
				ref.Kind = ReferenceArtist
			}
		}
		if ref.Influence == "" {
			ref.Influence = InfluencePositive
		}
		if ref.Query == "" && ref.TrackID == "" {
			continue
		}
		key := string(ref.Kind) + "\x00" + strings.ToLower(ref.Query) + "\x00" + ref.TrackID + "\x00" + string(ref.Influence)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func cleanResolution(in *ReferenceResolution) *ReferenceResolution {
	if in == nil {
		return nil
	}
	out := *in
	out.CatalogVersion = strings.TrimSpace(out.CatalogVersion)
	out.Alternatives = append([]ResolutionCandidate(nil), out.Alternatives...)
	if out.Selected != nil {
		selected := *out.Selected
		selected.Representatives = append([]WeightedTrack(nil), selected.Representatives...)
		out.Selected = &selected
	}
	return &out
}

func validateResolution(kind ReferenceKind, resolution ReferenceResolution) error {
	if resolution.Status != ResolutionResolved && resolution.Status != ResolutionAmbiguous && resolution.Status != ResolutionUnresolved {
		return fmt.Errorf("intent: invalid resolution status %q", resolution.Status)
	}
	if resolution.Status == ResolutionResolved && resolution.Selected == nil {
		return fmt.Errorf("intent: resolved reference has no selected entity")
	}
	if resolution.Selected != nil && resolution.Selected.Kind != kind {
		return fmt.Errorf("intent: resolution kind %q does not match reference kind %q", resolution.Selected.Kind, kind)
	}
	for _, candidate := range append(append([]ResolutionCandidate(nil), resolution.Alternatives...), dereferenceCandidate(resolution.Selected)...) {
		if candidate.Kind != kind || candidate.Confidence < 0 || candidate.Confidence > 1 {
			return fmt.Errorf("intent: invalid resolution candidate")
		}
		var total float64
		for _, representative := range candidate.Representatives {
			if strings.TrimSpace(representative.TrackID) == "" || representative.Weight <= 0 || representative.Weight > 1 {
				return fmt.Errorf("intent: invalid weighted representative")
			}
			total += representative.Weight
		}
		if len(candidate.Representatives) > 0 && math.Abs(total-1) > 0.001 {
			return fmt.Errorf("intent: representative weights must sum to one")
		}
	}
	return nil
}

func dereferenceCandidate(candidate *ResolutionCandidate) []ResolutionCandidate {
	if candidate == nil {
		return nil
	}
	return []ResolutionCandidate{*candidate}
}

func cleanPreferences(p SemanticPreferences) SemanticPreferences {
	p.Styles = cleanPreferenceList(p.Styles)
	p.Moods = cleanPreferenceList(p.Moods)
	p.Instrumentation = cleanPreferenceList(p.Instrumentation)
	p.TextureDescriptions = cleanPreferenceList(p.TextureDescriptions)
	if p.VocalPreference != nil {
		p.VocalPreference.Value = strings.TrimSpace(p.VocalPreference.Value)
		if p.VocalPreference.Influence == "" {
			p.VocalPreference.Influence = InfluencePositive
		}
		if p.VocalPreference.Value == "" {
			p.VocalPreference = nil
		}
	}
	return p
}

func cleanPreferenceList(in []IntentPreference) []IntentPreference {
	var out []IntentPreference
	for _, preference := range in {
		preference.Value = strings.TrimSpace(preference.Value)
		if preference.Value == "" {
			continue
		}
		if preference.Influence == "" {
			preference.Influence = InfluencePositive
		}
		out = append(out, preference)
	}
	return out
}

func cleanHardConstraints(in []HardConstraint) []HardConstraint {
	var out []HardConstraint
	for _, constraint := range in {
		constraint.Kind = strings.TrimSpace(constraint.Kind)
		constraint.Value = strings.TrimSpace(constraint.Value)
		if constraint.Kind != "" && constraint.Value != "" {
			constraint.Supported = HardConstraintSupported(constraint.Kind)
			out = append(out, constraint)
		}
	}
	return out
}

// HardConstraintSupported is the canonical enforcement registry used by all
// parsers and migrations. Unknown kinds are always preservation-only.
func HardConstraintSupported(kind string) bool {
	switch kind {
	case "exclude_artist", "exclude_reference_artists", "no_back_to_back_artist":
		return true
	default:
		return false
	}
}

func addUnsupportedConstraints(out []UnsupportedRequirement, constraints []HardConstraint) []UnsupportedRequirement {
	for _, constraint := range constraints {
		if constraint.Supported {
			continue
		}
		text := constraint.Value
		if len(constraint.Evidence) > 0 && constraint.Evidence[0].Text != "" {
			text = constraint.Evidence[0].Text
		}
		found := false
		for _, existing := range out {
			if strings.EqualFold(existing.Text, text) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, UnsupportedRequirement{
				Text: text, Reason: "the current catalog cannot enforce " + constraint.Kind,
				Evidence: constraint.Evidence,
			})
		}
	}
	return out
}

func cleanUnsupported(in []UnsupportedRequirement) []UnsupportedRequirement {
	var out []UnsupportedRequirement
	seen := map[string]struct{}{}
	for _, requirement := range in {
		requirement.Text = strings.TrimSpace(requirement.Text)
		requirement.Reason = strings.TrimSpace(requirement.Reason)
		if requirement.Text != "" {
			key := strings.ToLower(requirement.Text)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, requirement)
		}
	}
	return out
}

func positiveReferenceCount(refs []IntentReference) int {
	n := 0
	for _, ref := range refs {
		if ref.Influence == InfluencePositive {
			n++
		}
	}
	return n
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func seedSetEmpty(s IntentSeeds) bool { return len(s.Queries) == 0 && len(s.TrackIDs) == 0 }
func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
