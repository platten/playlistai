// Package schema defines the grammar-constrained local-model intent contract.
package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
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
	Genres               []WirePreference           `json:"genres"`
	Temporal             []core.TemporalRequirement `json:"temporal"`
	Destination          []WireReference            `json:"destination"`
	GenreExpansions      []core.GenreExpansion      `json:"genre_expansions"`
	References           []WireReference            `json:"references"`
	InferredAnchors      []WireAnchor               `json:"inferred_anchors"`
	RequiredTracks       []WireReference            `json:"required_tracks"`
	EssentialCriteria    []WireCriterion            `json:"essential_criteria"`
	Styles               []WirePreference           `json:"styles"`
	Moods                []WirePreference           `json:"moods"`
	Instrumentation      []WirePreference           `json:"instrumentation"`
	VocalPreference      WirePreference             `json:"vocal_preference"`
	Textures             []WirePreference           `json:"textures"`
	HardConstraints      []WireConstraint           `json:"hard_constraints"`
	Unsupported          []WireUnsupported          `json:"unsupported_requirements"`
	Mode                 string                     `json:"mode"`
	JourneyWaypoints     []WireReference            `json:"journey_waypoints"`
	EnergyTrajectory     []WireEnergy               `json:"energy_trajectory"`
	TotalCount           int                        `json:"total_count"`
	AudioWeight          float64                    `json:"audio_weight"`
	CooccurrenceWeight   float64                    `json:"cooccurrence_weight"`
	Discovery            float64                    `json:"discovery"`
	ArtistDiversity      float64                    `json:"artist_diversity"`
	TransitionSmoothness float64                    `json:"transition_smoothness"`
	Notes                string                     `json:"notes"`
}

// Every rule body is one physical line for the pinned llama.cpp parser.
const GBNF = `root ::= "{" ws "\"genres\":" ws preflist ws "," ws "\"temporal\":" ws temporallist ws "," ws "\"destination\":" ws reflist ws "," ws "\"genre_expansions\":" ws genrelist ws "," ws "\"references\":" ws reflist ws "," ws "\"inferred_anchors\":" ws anchorlist ws "," ws "\"required_tracks\":" ws reflist ws "," ws "\"essential_criteria\":" ws criterionlist ws "," ws "\"styles\":" ws preflist ws "," ws "\"moods\":" ws preflist ws "," ws "\"instrumentation\":" ws preflist ws "," ws "\"vocal_preference\":" ws pref ws "," ws "\"textures\":" ws preflist ws "," ws "\"hard_constraints\":" ws hardlist ws "," ws "\"unsupported_requirements\":" ws unsupportedlist ws "," ws "\"mode\":" ws ("\"similar\"" | "\"journey\"") ws "," ws "\"journey_waypoints\":" ws reflist ws "," ws "\"energy_trajectory\":" ws energylist ws "," ws "\"total_count\":" ws int ws "," ws "\"audio_weight\":" ws num ws "," ws "\"cooccurrence_weight\":" ws num ws "," ws "\"discovery\":" ws num ws "," ws "\"artist_diversity\":" ws num ws "," ws "\"transition_smoothness\":" ws num ws "," ws "\"notes\":" ws str ws "}" ws
temporallist ::= "[" ws (period (ws "," ws period){0,2})? ws "]"
period ::= "{" ws "\"basis\":" ws ("\"composition\"" | "\"original_release\"") ws "," ws "\"startYear\":" ws int ws "," ws "\"endYear\":" ws int ws "," ws "\"scope\":" ws ("\"playlist\"" | "\"journey_start\"" | "\"journey_end\"") ws "}"
genrelist ::= "[" ws (genre (ws "," ws genre){0,2})? ws "]"
genre ::= "{" ws "\"genre\":" ws str ws "," ws "\"characteristics\":" ws str ws "," ws "\"relatedGenres\":" ws stringlist ws "}"
stringlist ::= "[" ws (str (ws "," ws str (ws "," ws str)?)?)? ws "]"
reflist ::= "[" ws (ref (ws "," ws ref){0,7})? ws "]"
ref ::= "{" ws "\"kind\":" ws ("\"artist\"" | "\"track\"" | "\"album\"") ws "," ws "\"value\":" ws str ws "," ws "\"influence\":" ws ("\"positive\"" | "\"negative\"") ws "," ws "\"explicit\":" ws bool ws "," ws "\"span\":" ws str ws "}"
anchorlist ::= "[" ws (anchor (ws "," ws anchor (ws "," ws anchor)?)?)? ws "]"
anchor ::= "{" ws "\"kind\":" ws ("\"artist\"" | "\"track\"" | "\"album\"") ws "," ws "\"value\":" ws str ws "," ws "\"role\":" ws str ws "," ws "\"reason\":" ws str ws "," ws "\"span\":" ws str ws "}"
criterionlist ::= "[" ws (criterion (ws "," ws criterion){0,7})? ws "]"
criterion ::= "{" ws "\"kind\":" ws ("\"genre\"" | "\"texture\"" | "\"style\"" | "\"mood\"" | "\"instrumentation\"" | "\"vocal\"") ws "," ws "\"value\":" ws str ws "," ws "\"scope\":" ws ("\"playlist\"" | "\"journey_start\"" | "\"journey_end\"" | "\"journey_via\"") ws "," ws "\"span\":" ws str ws "}"
preflist ::= "[" ws (pref (ws "," ws pref){0,7})? ws "]"
pref ::= "{" ws "\"value\":" ws str ws "," ws "\"influence\":" ws ("\"positive\"" | "\"negative\"") ws "," ws "\"explicit\":" ws bool ws "," ws "\"span\":" ws str ws "}"
hardlist ::= "[" ws (hard (ws "," ws hard){0,7})? ws "]"
hard ::= "{" ws "\"kind\":" ws str ws "," ws "\"value\":" ws str ws "," ws "\"span\":" ws str ws "}"
unsupportedlist ::= "[" ws (unsupported (ws "," ws unsupported){0,7})? ws "]"
unsupported ::= "{" ws "\"text\":" ws str ws "," ws "\"reason\":" ws str ws "," ws "\"span\":" ws str ws "}"
energylist ::= "[" ws (energy (ws "," ws energy){0,7})? ws "]"
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
		if prompt != "" {
			return core.MusicIntent{}, fmt.Errorf("schema: legacy model output cannot preserve the current request contract")
		}
		return parseLegacy(obj)
	}
	var wire Wire
	dec := json.NewDecoder(bytes.NewReader(obj))
	if err := dec.Decode(&wire); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	if prompt != "" && wire.Genres != nil {
		discardInventedInstructions(&wire, prompt)
		preserveCategoryJourney(&wire, prompt)
		normalizePeriods(&wire, prompt)
		preserveQualityClauses(&wire, prompt)
		if err := validateOpenIntent(wire, prompt); err != nil {
			return core.MusicIntent{}, err
		}
	}
	if prompt != "" && wire.Genres == nil {
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
	intent.OriginalDescription = prompt
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
	expected, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
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
	for _, preference := range expected.Preferences.Styles {
		style := preference.Value
		negative := preference.Influence == core.InfluenceNegative
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
	// The fallback parser and model validator must agree on defining meaning,
	// including category+reference requests and songs/tracks wording.
	intent, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	result := make([]definingStyle, 0, len(intent.EssentialCriteria))
	for _, criterion := range intent.EssentialCriteria {
		result = append(result, definingStyle{normalizeStyleLabel(criterion.Value), criterion.Scope})
	}
	return result
}

func normalizeStyleLabel(value string) string {
	return core.CanonicalStyle(strings.ToLower(strings.Join(strings.Fields(value), " ")))
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
	if w.Genres != nil {
		// Instrumental describes vocal presence rather than an instrument.
		instrumentation := make([]WirePreference, 0, len(w.Instrumentation))
		for _, preference := range w.Instrumentation {
			if strings.EqualFold(preference.Value, "instrumental") && w.VocalPreference.Value == "" {
				w.VocalPreference = preference
			} else {
				instrumentation = append(instrumentation, preference)
			}
		}
		w.Instrumentation = instrumentation
		valid := make([]WireAnchor, 0, len(w.InferredAnchors))
		for _, anchor := range w.InferredAnchors {
			if anchor.Kind == "track" && strings.Contains(anchor.Value, " - ") {
				valid = append(valid, anchor)
			}
		}
		w.InferredAnchors = valid
	}
	references, migratedAnchors := splitReferences(w.References)
	intent := core.MusicIntent{
		GenreExpansions:   w.GenreExpansions,
		Temporal:          w.Temporal,
		Version:           core.CurrentIntentVersion,
		References:        references,
		InferredAnchors:   append(migratedAnchors, anchorsToCore(w.InferredAnchors)...),
		RequiredTracks:    referencesToCore(w.RequiredTracks),
		EssentialCriteria: criteriaToCore(w.EssentialCriteria),
		Preferences: core.SemanticPreferences{
			Styles:              preferencesToCore(w.Styles),
			Genres:              preferencesToCore(w.Genres),
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
	for _, genre := range intent.Preferences.Genres {
		if genre.Influence == core.InfluenceNegative {
			continue
		}
		found := false
		for _, criterion := range intent.EssentialCriteria {
			if strings.EqualFold(criterion.Value, genre.Value) {
				found = true
			}
		}
		soft := false
		for _, e := range genre.Evidence {
			soft = soft || strings.Contains(strings.ToLower(e.Text), "influence") || strings.Contains(strings.ToLower(e.Text), "touch of")
		}
		if !found && !soft {
			scope := "playlist"
			if intent.Mode == core.ModeJourney {
				scope = "journey_start"
			}
			intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: genre.Value, Scope: scope, Evidence: genre.Evidence})
		}
	}
	if w.Genres == nil {
		intent.VerificationPolicy = core.VerifiedOnly
	} else {
		// Expansion roots are optional model suggestions. A hallucinated root
		// must neither replace a requested category nor invalidate that request.
		intent.GenreExpansions = nil
		seen := map[string]bool{}
		for _, expansion := range w.GenreExpansions {
			key := core.NormalizeIdentityPart(expansion.Genre)
			for _, criterion := range intent.EssentialCriteria {
				if !seen[key] && key == core.NormalizeIdentityPart(criterion.Value) && (criterion.Kind == "genre" || criterion.Kind == "style") {
					intent.GenreExpansions = append(intent.GenreExpansions, expansion)
					seen[key] = true
				}
			}
		}
	}
	if len(w.Destination) == 1 {
		d := referenceToCore(w.Destination[0])
		intent.Destination = &d
		intent.Mode = core.ModeJourney
		intent.Journey.Waypoints = append(intent.Journey.Waypoints, d)
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
	expected, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	for _, reference := range w.RequiredTracks {
		if !reference.Explicit {
			return fmt.Errorf("schema: required track %q was not explicitly requested", reference.Value)
		}
	}
	groups := [][]WireReference{w.References, w.RequiredTracks, w.JourneyWaypoints}
	for _, group := range groups {
		for _, reference := range group {
			if !reference.Explicit {
				continue
			}
			value := strings.ToLower(strings.TrimSpace(reference.Value))
			span := strings.ToLower(strings.TrimSpace(reference.Span))
			if value == "" || span == "" || !containsReferenceWords(prompt, span) || !referenceGrounded(span, reference) {
				return fmt.Errorf("schema: explicit reference %q has no evidence in the user request", reference.Value)
			}
			if reference.Kind == "artist" {
				for _, criterion := range expected.EssentialCriteria {
					if normalizeStyleLabel(value) != normalizeStyleLabel(criterion.Value) {
						continue
					}
					explicitlyNamed := false
					for _, ref := range expected.References {
						if containsReferenceWords(ref.Query, value) {
							explicitlyNamed = true
						}
					}
					if !explicitlyNamed {
						return fmt.Errorf("schema: artist reference %q conflicts with category wording in the user request", reference.Value)
					}
				}
			}
		}
	}
	return nil
}

func referenceGrounded(text string, ref WireReference) bool {
	if containsReferenceWords(text, ref.Value) {
		return true
	}
	if ref.Kind != "album" && ref.Kind != "track" {
		return false
	}
	artist, title, qualified := core.QualifiedReferenceParts(ref.Value)
	return qualified && containsReferenceWords(text, artist) && containsReferenceWords(text, title)
}

func containsReferenceWords(text, reference string) bool {
	words := func(value string) string {
		// Models may restore accents omitted by the listener. Match the
		// catalog's Unicode normalization before testing source grounding,
		// retaining word boundaries and every script rather than fuzzy names.
		value = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Mn, r) {
				return -1
			}
			return unicode.ToLower(r)
		}, norm.NFKD.String(value))
		return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}), " ")
	}
	want := words(reference)
	return want != "" && strings.Contains(" "+words(text)+" ", " "+want+" ")
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
