// Package schema defines the JSON shape a local LLM emits for a parsed intent,
// the GBNF grammar that constrains it, and the mapping back to core.MusicIntent.
// The rules parser does not use this — it builds a core.MusicIntent directly —
// but the two are kept semantically aligned.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

// Wire is the exact object the model produces (fixed key order, see GBNF).
type Wire struct {
	Seeds          []string `json:"seeds"`
	Mode           string   `json:"mode"` // "similar" | "journey"
	Count          int      `json:"count"`
	Creativity     float64  `json:"creativity"`
	Noise          float64  `json:"noise"`
	Lookback       int      `json:"lookback"`
	ExcludeArtists []string `json:"exclude_artists"`
	NoRepeatArtist bool     `json:"no_repeat_artist"`
	Notes          string   `json:"notes"`
}

// GBNF is the llama.cpp grammar for Wire. Keys are emitted in a fixed order so
// the grammar (and constrained decoding) stays simple; the model must emit all
// of them.
//
// Every rule body is on a single physical line: the pinned llama-server's GBNF
// parser rejects rules whose body wraps across lines ("failed to parse
// grammar"). Do not reflow this for readability.
const GBNF = `root ::= "{" ws "\"seeds\":" ws strlist ws "," ws "\"mode\":" ws ("\"similar\"" | "\"journey\"") ws "," ws "\"count\":" ws int ws "," ws "\"creativity\":" ws num ws "," ws "\"noise\":" ws num ws "," ws "\"lookback\":" ws int ws "," ws "\"exclude_artists\":" ws strlist ws "," ws "\"no_repeat_artist\":" ws ("true" | "false") ws "," ws "\"notes\":" ws str ws "}" ws
strlist ::= "[" ws ( str (ws "," ws str)* )? ws "]"
str ::= "\"" ( [^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F]) )* "\""
int ::= "-"? ("0" | [1-9] [0-9]*)
num ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)?
ws ::= [ \t\n]*`

// Parse turns a model response (which may be fenced or wrapped in prose) into a
// normalized core.MusicIntent.
func Parse(raw []byte) (core.MusicIntent, error) {
	obj, ok := extractObject(raw)
	if !ok {
		return core.MusicIntent{}, fmt.Errorf("schema: no JSON object in response")
	}
	var w Wire
	dec := json.NewDecoder(bytes.NewReader(obj))
	if err := dec.Decode(&w); err != nil {
		return core.MusicIntent{}, fmt.Errorf("schema: %w", err)
	}
	return w.ToCore(), nil
}

// ToCore maps the wire object onto core.MusicIntent and normalizes it.
func (w Wire) ToCore() core.MusicIntent {
	m := core.MusicIntent{
		Version:    1,
		Seeds:      core.IntentSeeds{Queries: cleanStrings(w.Seeds)},
		Mode:       core.Mode(w.Mode),
		Count:      w.Count,
		Creativity: w.Creativity,
		Noise:      w.Noise,
		Lookback:   w.Lookback,
		Constraints: core.IntentConstraints{
			NoRepeatArtistBackToBack: w.NoRepeatArtist,
			ArtistsExclude:           cleanStrings(w.ExcludeArtists),
		},
		NotesForUser: w.Notes,
	}
	out := m.Normalized()
	// Normalized() flips an unset/invalid mode to journey for >=2 seeds; honor
	// an explicit "similar" from the model instead.
	if w.Mode == string(core.ModeSimilar) || w.Mode == string(core.ModeJourney) {
		out.Mode = core.Mode(w.Mode)
	}
	return out
}

// extractObject returns the first balanced {...} span in raw.
func extractObject(raw []byte) ([]byte, bool) {
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		return nil, false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// skip
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], true
			}
		}
	}
	return nil, false
}

func cleanStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
