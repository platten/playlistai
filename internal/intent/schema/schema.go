// Package schema defines the grammar-constrained local-model intent contract.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

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
	RequiredTracks       []WireReference   `json:"required_tracks"`
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
const GBNF = `root ::= "{" ws "\"references\":" ws reflist ws "," ws "\"required_tracks\":" ws reflist ws "," ws "\"styles\":" ws preflist ws "," ws "\"moods\":" ws preflist ws "," ws "\"instrumentation\":" ws preflist ws "," ws "\"vocal_preference\":" ws pref ws "," ws "\"textures\":" ws preflist ws "," ws "\"hard_constraints\":" ws hardlist ws "," ws "\"unsupported_requirements\":" ws unsupportedlist ws "," ws "\"mode\":" ws ("\"similar\"" | "\"journey\"") ws "," ws "\"journey_waypoints\":" ws reflist ws "," ws "\"energy_trajectory\":" ws energylist ws "," ws "\"total_count\":" ws int ws "," ws "\"audio_weight\":" ws num ws "," ws "\"cooccurrence_weight\":" ws num ws "," ws "\"discovery\":" ws num ws "," ws "\"artist_diversity\":" ws num ws "," ws "\"transition_smoothness\":" ws num ws "," ws "\"notes\":" ws str ws "}" ws
reflist ::= "[" ws (ref (ws "," ws ref)*)? ws "]"
ref ::= "{" ws "\"kind\":" ws ("\"artist\"" | "\"track\"") ws "," ws "\"value\":" ws str ws "," ws "\"influence\":" ws ("\"positive\"" | "\"negative\"") ws "," ws "\"explicit\":" ws bool ws "," ws "\"span\":" ws str ws "}"
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
	intent := wire.ToCore()
	if err := intent.Validate(); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	return intent.Normalized(), nil
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
	intent := core.MusicIntent{
		Version:        core.CurrentIntentVersion,
		References:     referencesToCore(w.References),
		RequiredTracks: referencesToCore(w.RequiredTracks),
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

func referencesToCore(in []WireReference) []core.IntentReference {
	out := make([]core.IntentReference, 0, len(in))
	for _, ref := range in {
		out = append(out, core.IntentReference{
			Kind: core.ReferenceKind(ref.Kind), Query: ref.Value,
			Influence: core.Influence(ref.Influence), Evidence: evidence(ref.Span, ref.Explicit),
		})
	}
	return out
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
