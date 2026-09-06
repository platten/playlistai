// Package schema defines the grammar-constrained local-model intent contract.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// Version keys parsed-intent reuse. It follows the core contract because every
// schema change that affects interpretation increments that contract version.
const Version = core.CurrentIntentVersion

type WireReference struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Influence string `json:"influence"`
	Explicit  bool   `json:"explicit"`
	Span      string `json:"span"`
}

type WirePreference struct {
	Value     string `json:"value"`
	Influence string `json:"influence"`
	Explicit  bool   `json:"explicit"`
	Span      string `json:"span"`
}

type WireCriterion struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Scope string `json:"scope"`
	Span  string `json:"span"`
}

type WireAnchor struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
	Span   string `json:"span"`
}

type WireConstraint struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Span  string `json:"span"`
}

type WireUnsupported struct {
	Text   string `json:"text"`
	Reason string `json:"reason"`
	Span   string `json:"span"`
}

type WireEnergy struct {
	Position float64 `json:"position"`
	Energy   float64 `json:"energy"`
}

// Wire is the exact fixed-order object emitted by the local model.
type Wire struct {
	References           []WireReference   `json:"references"`
	InferredAnchors      []WireAnchor      `json:"inferred_anchors"`
	RequiredTracks       []WireReference   `json:"required_tracks"`
	EssentialCriteria    []WireCriterion   `json:"essential_criteria"`
	Styles               []WirePreference  `json:"styles"`
	Moods                []WirePreference  `json:"moods"`
	Instrumentation      []WirePreference  `json:"instrumentation"`
	VocalPreference      WirePreference    `json:"vocal_preference"`
	Textures             []WirePreference  `json:"textures"`
	HardConstraints      []WireConstraint  `json:"hard_constraints"`
	Unsupported          []WireUnsupported `json:"unsupported_requirements"`
	Mode                 string            `json:"mode"`
	JourneyWaypoints     []WireReference   `json:"journey_waypoints"`
	EnergyTrajectory     []WireEnergy      `json:"energy_trajectory"`
	TotalCount           int               `json:"total_count"`
	AudioWeight          float64           `json:"audio_weight"`
	CooccurrenceWeight   float64           `json:"cooccurrence_weight"`
	Discovery            float64           `json:"discovery"`
	ArtistDiversity      float64           `json:"artist_diversity"`
	TransitionSmoothness float64           `json:"transition_smoothness"`
	Notes                string            `json:"notes"`
}

// Every rule body is one physical line for the pinned llama.cpp parser.
const GBNF = `root ::= "{" ws "\"references\":" ws reflist ws "," ws "\"inferred_anchors\":" ws anchorlist ws "," ws "\"required_tracks\":" ws reflist ws "," ws "\"essential_criteria\":" ws criterionlist ws "," ws "\"styles\":" ws preflist ws "," ws "\"moods\":" ws preflist ws "," ws "\"instrumentation\":" ws preflist ws "," ws "\"vocal_preference\":" ws pref ws "," ws "\"textures\":" ws preflist ws "," ws "\"hard_constraints\":" ws hardlist ws "," ws "\"unsupported_requirements\":" ws unsupportedlist ws "," ws "\"mode\":" ws ("\"similar\"" | "\"journey\"") ws "," ws "\"journey_waypoints\":" ws reflist ws "," ws "\"energy_trajectory\":" ws energylist ws "," ws "\"total_count\":" ws int ws "," ws "\"audio_weight\":" ws num ws "," ws "\"cooccurrence_weight\":" ws num ws "," ws "\"discovery\":" ws num ws "," ws "\"artist_diversity\":" ws num ws "," ws "\"transition_smoothness\":" ws num ws "," ws "\"notes\":" ws str ws "}" ws
reflist ::= "[" ws (ref (ws "," ws ref)*)? ws "]"
ref ::= "{" ws "\"kind\":" ws ("\"artist\"" | "\"track\"") ws "," ws "\"value\":" ws str ws "," ws "\"influence\":" ws ("\"positive\"" | "\"negative\"") ws "," ws "\"explicit\":" ws bool ws "," ws "\"span\":" ws str ws "}"
anchorlist ::= "[" ws (anchor (ws "," ws anchor)*)? ws "]"
anchor ::= "{" ws "\"kind\":" ws ("\"artist\"" | "\"track\"") ws "," ws "\"value\":" ws str ws "," ws "\"role\":" ws str ws "," ws "\"reason\":" ws str ws "," ws "\"span\":" ws str ws "}"
criterionlist ::= "[" ws (criterion (ws "," ws criterion)*)? ws "]"
criterion ::= "{" ws "\"kind\":" ws ("\"style\"" | "\"mood\"" | "\"instrumentation\"" | "\"vocal\"") ws "," ws "\"value\":" ws str ws "," ws "\"scope\":" ws ("\"playlist\"" | "\"journey_start\"" | "\"journey_end\"" | "\"journey_via\"") ws "," ws "\"span\":" ws str ws "}"
preflist ::= "[" ws (pref (ws "," ws pref)*)? ws "]"
pref ::= "{" ws "\"value\":" ws str ws "," ws "\"influence\":" ws ("\"positive\"" | "\"negative\"") ws "," ws "\"explicit\":" ws bool ws "," ws "\"span\":" ws str ws "}"
hardlist ::= "[" ws (hard (ws "," ws hard)*)? ws "]"
hard ::= "{" ws "\"kind\":" ws str ws "," ws "\"value\":" ws str ws "," ws "\"span\":" ws str ws "}"
unsupportedlist ::= "[" ws (unsupported (ws "," ws unsupported)*)? ws "]"
unsupported ::= "{" ws "\"text\":" ws str ws "," ws "\"reason\":" ws str ws "," ws "\"span\":" ws str ws "}"
energylist ::= "[" ws (energy (ws "," ws energy)*)? ws "]"
energy ::= "{" ws "\"position\":" ws num ws "," ws "\"energy\":" ws num ws "}"
bool ::= "true" | "false"
str ::= "\"" ( [^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F]) )* "\""
int ::= "-"? ("0" | [1-9] [0-9]*)
num ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)?
ws ::= [ \t\n]*`

func Parse(raw []byte) (core.MusicIntent, error) {
	return parse(raw, "")
}

// ParseForPrompt additionally verifies that every reference marked explicit
// is grounded in the user's text. Model confidence cannot manufacture a user
// instruction that was never present.
func ParseForPrompt(raw []byte, prompt string) (core.MusicIntent, error) {
	return parse(raw, prompt)
}

func parse(raw []byte, prompt string) (core.MusicIntent, error) {
	obj, ok := extractObject(raw)
	if !ok {
		return core.MusicIntent{}, fmt.Errorf("schema: no JSON object in response")
	}
	if bytes.Contains(obj, []byte(`"seeds"`)) && !bytes.Contains(obj, []byte(`"references"`)) {
		return parseLegacy(obj)
	}
	var wire Wire
	dec := json.NewDecoder(bytes.NewReader(obj))
	if err := dec.Decode(&wire); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	if prompt != "" {
		if err := validateExplicitReferences(wire, prompt); err != nil {
			return core.MusicIntent{}, err
		}
		if err := validateDefiningMeaning(wire, prompt); err != nil {
			return core.MusicIntent{}, err
		}
		if err := validateKnownModifiers(wire, prompt); err != nil {
			return core.MusicIntent{}, err
		}
	}
	intent := wire.ToCore()
	if err := intent.Validate(); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	return intent.Normalized(), nil
}

func validateDefiningMeaning(w Wire, prompt string) error {
	for _, expected := range definingStyleCriteria(prompt) {
		matched := false
		for _, criterion := range w.EssentialCriteria {
			span := strings.ToLower(strings.TrimSpace(criterion.Span))
			if criterion.Kind == "style" && normalizeStyleLabel(criterion.Value) == expected.value && criterion.Scope == expected.scope && span != "" && strings.Contains(strings.ToLower(prompt), span) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("schema: defining category %q was not preserved as essential for %s", expected.value, expected.scope)
		}
	}
	return nil
}

func validateKnownModifiers(w Wire, prompt string) error {
	lower := strings.ToLower(strings.Join(strings.Fields(prompt), " "))
	styles := []string{"ambient electronic", "rock & roll", "rock and roll", "abstract drone", "electronic", "ambient", "techno", "jazz", "folk", "rock", "drone"}
	hasPreference := func(style, influence string) bool {
		want := normalizeStyleLabel(style)
		for _, preference := range w.Styles {
			if normalizeStyleLabel(preference.Value) == want && preference.Influence == influence && preference.Explicit && strings.Contains(lower, strings.ToLower(strings.TrimSpace(preference.Span))) {
				return strings.TrimSpace(preference.Span) != ""
			}
		}
		return false
	}
	hasConstraint := func(style string) bool {
		want := normalizeStyleLabel(style)
		for _, constraint := range w.HardConstraints {
			if constraint.Kind == "exclude_style" && normalizeStyleLabel(constraint.Value) == want && strings.TrimSpace(constraint.Span) != "" && strings.Contains(lower, strings.ToLower(strings.TrimSpace(constraint.Span))) {
				return true
			}
		}
		return false
	}
	for _, style := range styles {
		negative := strings.Contains(lower, "no "+style) || strings.Contains(lower, "without "+style) || strings.Contains(lower, "not "+style)
		if negative && (!hasPreference(style, "negative") || !hasConstraint(style)) {
			return fmt.Errorf("schema: explicit style exclusion %q was not preserved as a negative preference and hard exclusion", normalizeStyleLabel(style))
		}
		influence := strings.Contains(lower, "some "+style+" influence") || strings.Contains(lower, style+" influence") || strings.Contains(lower, "touch of "+style)
		if influence && !hasPreference(style, "positive") {
			return fmt.Errorf("schema: deliberate style influence %q was not preserved as a soft positive preference", normalizeStyleLabel(style))
		}
	}
	return nil
}

type definingStyle struct{ value, scope string }

// definingStyleCriteria is deliberately reviewed and conservative. It guards
// clear genre-led requests without promoting every descriptive adjective to a
// hard condition. Cross-genre influence wording remains a soft preference.
func definingStyleCriteria(prompt string) []definingStyle {
	lower := strings.ToLower(strings.Join(strings.Fields(prompt), " "))
	styles := []string{"ambient electronic", "rock & roll", "rock and roll", "electronic", "ambient", "techno", "jazz", "folk", "rock", "drone"}
	canonical := normalizeStyleLabel
	if from := strings.Index(lower, "from "); from >= 0 {
		journey := lower[from+len("from "):]
		if split := strings.Index(journey, " to "); split >= 0 {
			left, right := strings.TrimSpace(journey[:split]), strings.TrimSpace(journey[split+len(" to "):])
			for _, suffix := range []string{" music", " songs", " tracks"} {
				left, right = strings.TrimSuffix(left, suffix), strings.TrimSuffix(right, suffix)
			}
			if knownStyle(styles, left) && knownStyle(styles, right) {
				return []definingStyle{{canonical(left), "journey_start"}, {canonical(right), "journey_end"}}
			}
		}
	}
	for _, style := range styles {
		phrase := style + " music"
		if strings.Contains(lower, phrase) && !strings.Contains(lower, "music by "+style) && !strings.Contains(lower, "like "+style) {
			return []definingStyle{{canonical(style), "playlist"}}
		}
	}
	return nil
}

func knownStyle(styles []string, value string) bool {
	for _, style := range styles {
		if value == style {
			return true
		}
	}
	return false
}

func normalizeStyleLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "rock and roll" {
		return "rock & roll"
	}
	return value
}

type legacyWire struct {
	Seeds              []string `json:"seeds"`
	RequiredTracks     []string `json:"required_tracks"`
	Mode               string   `json:"mode"`
	Count              int      `json:"count"`
	Creativity         float64  `json:"creativity"`
	Noise              float64  `json:"noise"`
	Lookback           int      `json:"lookback"`
	ExcludeArtists     []string `json:"exclude_artists"`
	NoRepeatArtist     bool     `json:"no_repeat_artist"`
	ExcludeSeedArtists bool     `json:"exclude_seed_artists"`
	Notes              string   `json:"notes"`
}

func parseLegacy(obj []byte) (core.MusicIntent, error) {
	var wire legacyWire
	if err := json.Unmarshal(obj, &wire); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	return core.MusicIntent{
		Version:  2,
		Seeds:    core.IntentSeeds{Queries: wire.Seeds},
		Required: core.IntentSeeds{Queries: wire.RequiredTracks},
		Mode:     core.Mode(wire.Mode), Count: wire.Count, Creativity: wire.Creativity,
		Noise: wire.Noise, Lookback: wire.Lookback,
		Constraints: core.IntentConstraints{
			ArtistsExclude:           wire.ExcludeArtists,
			NoRepeatArtistBackToBack: wire.NoRepeatArtist,
			ExcludeSeedArtists:       wire.ExcludeSeedArtists,
		},
		NotesForUser: wire.Notes,
	}.Normalized(), nil
}

func (w Wire) ToCore() core.MusicIntent {
	references, migratedAnchors := splitReferences(w.References)
	intent := core.MusicIntent{
		Version:           core.CurrentIntentVersion,
		References:        references,
		InferredAnchors:   append(migratedAnchors, anchorsToCore(w.InferredAnchors)...),
		RequiredTracks:    referencesToCore(w.RequiredTracks),
		EssentialCriteria: criteriaToCore(w.EssentialCriteria),
		Preferences: core.SemanticPreferences{
			Styles:              preferencesToCore(w.Styles),
			Moods:               preferencesToCore(w.Moods),
			Instrumentation:     preferencesToCore(w.Instrumentation),
			TextureDescriptions: preferencesToCore(w.Textures),
		},
		Mode: core.Mode(w.Mode),
		Controls: core.IntentControls{
			TotalTrackCount:      w.TotalCount,
			AudioWeight:          w.AudioWeight,
			CooccurrenceWeight:   w.CooccurrenceWeight,
			Discovery:            w.Discovery,
			ArtistDiversity:      w.ArtistDiversity,
			TransitionSmoothness: w.TransitionSmoothness,
		},
		Journey: core.JourneyPlan{
			Waypoints:        referencesToCore(w.JourneyWaypoints),
			EnergyTrajectory: energyToCore(w.EnergyTrajectory),
		},
		Unsupported:         interpretationUnsupported(w.Unsupported),
		NotesForUser:        w.Notes,
		InterpretationNotes: w.Notes,
	}
	if strings.TrimSpace(w.VocalPreference.Value) != "" {
		preference := preferenceToCore(w.VocalPreference)
		intent.Preferences.VocalPreference = &preference
	}
	for _, constraint := range w.HardConstraints {
		supported := core.HardConstraintSupported(constraint.Kind)
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: constraint.Kind, Value: constraint.Value, Supported: supported,
			Evidence: evidence(constraint.Span, true),
		})
		if !supported {
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{
				Text: constraint.Span, Reason: "the current catalog cannot enforce " + constraint.Kind,
				Evidence: evidence(constraint.Span, true),
			})
		}
	}
	return intent
}

func splitReferences(in []WireReference) ([]core.IntentReference, []core.InferredAnchor) {
	var references []core.IntentReference
	var anchors []core.InferredAnchor
	for _, ref := range in {
		converted := referenceToCore(ref)
		if !ref.Explicit {
			anchors = append(anchors, core.InferredAnchor{
				Reference: converted, Role: "retrieval", Reason: "model-proposed legacy reference",
				Suitability: core.AnchorSuitability{State: core.EvidenceUnknown},
			})
			continue
		}
		references = append(references, converted)
	}
	return references, anchors
}

func anchorsToCore(in []WireAnchor) []core.InferredAnchor {
	out := make([]core.InferredAnchor, 0, len(in))
	for _, anchor := range in {
		out = append(out, core.InferredAnchor{
			Reference: core.IntentReference{Kind: core.ReferenceKind(anchor.Kind), Query: anchor.Value, Influence: core.InfluencePositive, Evidence: evidence(anchor.Span, false)},
			Role:      anchor.Role, Reason: anchor.Reason,
			Suitability: core.AnchorSuitability{State: core.EvidenceUnknown, Detail: "awaiting grounded catalog validation"},
		})
	}
	return out
}

func criteriaToCore(in []WireCriterion) []core.MusicalCriterion {
	out := make([]core.MusicalCriterion, 0, len(in))
	for _, criterion := range in {
		out = append(out, core.MusicalCriterion{Kind: criterion.Kind, Value: criterion.Value, Scope: criterion.Scope, Evidence: evidence(criterion.Span, true)})
	}
	return out
}

func referencesToCore(in []WireReference) []core.IntentReference {
	out := make([]core.IntentReference, 0, len(in))
	for _, ref := range in {
		out = append(out, referenceToCore(ref))
	}
	return out
}

func referenceToCore(ref WireReference) core.IntentReference {
	return core.IntentReference{
		Kind: core.ReferenceKind(ref.Kind), Query: ref.Value,
		Influence: core.Influence(ref.Influence), Evidence: evidence(ref.Span, ref.Explicit),
	}
}

func validateExplicitReferences(w Wire, prompt string) error {
	lower := strings.ToLower(prompt)
	groups := [][]WireReference{w.References, w.RequiredTracks, w.JourneyWaypoints}
	for _, group := range groups {
		for _, reference := range group {
			if !reference.Explicit {
				continue
			}
			value := strings.ToLower(strings.TrimSpace(reference.Value))
			span := strings.ToLower(strings.TrimSpace(reference.Span))
			if value == "" || (!strings.Contains(lower, value) && (span == "" || !strings.Contains(lower, span))) {
				return fmt.Errorf("schema: explicit reference %q has no evidence in the user request", reference.Value)
			}
			categoryUse := strings.Contains(lower, value+" music") &&
				!strings.Contains(lower, "by "+value) && !strings.Contains(lower, "like "+value) &&
				!strings.Contains(lower, "artist "+value)
			if reference.Kind == "artist" && categoryUse {
				return fmt.Errorf("schema: artist reference %q conflicts with category wording in the user request", reference.Value)
			}
		}
	}
	return nil
}

func preferencesToCore(in []WirePreference) []core.IntentPreference {
	out := make([]core.IntentPreference, 0, len(in))
	for _, preference := range in {
		out = append(out, preferenceToCore(preference))
	}
	return out
}

func preferenceToCore(p WirePreference) core.IntentPreference {
	return core.IntentPreference{
		Value: p.Value, Influence: core.Influence(p.Influence),
		Explicit: p.Explicit, Evidence: evidence(p.Span, p.Explicit),
	}
}

func energyToCore(in []WireEnergy) []core.EnergyPoint {
	out := make([]core.EnergyPoint, 0, len(in))
	for _, point := range in {
		out = append(out, core.EnergyPoint{Position: point.Position, Energy: point.Energy})
	}
	return out
}

func interpretationUnsupported(in []WireUnsupported) []core.UnsupportedRequirement {
	out := make([]core.UnsupportedRequirement, 0, len(in))
	for _, requirement := range in {
		out = append(out, core.UnsupportedRequirement{
			Text: requirement.Text, Reason: requirement.Reason, Evidence: evidence(requirement.Span, true),
		})
	}
	return out
}

func evidence(span string, explicit bool) []core.SourceEvidence {
	span = strings.TrimSpace(span)
	if span == "" {
		return nil
	}
	return []core.SourceEvidence{{Text: span, Start: -1, End: -1, Explicit: explicit}}
}

func extractObject(raw []byte) ([]byte, bool) {
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		return nil, false
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(raw); i++ {
		char := raw[i]
		switch {
		case escaped:
			escaped = false
		case char == '\\' && inString:
			escaped = true
		case char == '"':
			inString = !inString
		case inString:
		case char == '{':
			depth++
		case char == '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], true
			}
		}
	}
	return nil, false
}
