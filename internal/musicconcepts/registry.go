// Package musicconcepts holds the reviewed, provider-independent musical
// vocabulary. It identifies words; it never establishes a recording's fit.
package musicconcepts

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const Version = "music-concepts/v1"

// ProviderMappings keeps provider class names out of the user's intent. Parent
// and related concepts are never expanded by Canonical or Find.
type ProviderMappings struct {
	MusicBrainz    []string          `json:"musicBrainz,omitempty"`
	AcousticBrainz map[string]string `json:"acousticBrainz,omitempty"`
	CLAP           []string          `json:"clap,omitempty"`
	DSP            []string          `json:"dsp,omitempty"`
}

type Concept struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	Value     string           `json:"value"`
	Aliases   []string         `json:"aliases,omitempty"`
	Parents   []string         `json:"parents,omitempty"`
	Related   []string         `json:"related,omitempty"`
	Providers ProviderMappings `json:"providers,omitempty"`
	Source    string           `json:"source"`
	License   string           `json:"license"`
}

// The small hand-reviewed registry contains no model weights, audio, user data
// or downloaded provider database. Desktop loading uses only the Go standard
// library; the optional preparation script is an offline contributor tool.
//
//go:embed concepts.json
var registryJSON []byte

var entries, byAlias, byID = loadRegistry()

func kindKey(kind string) string {
	kind = normalize(kind)
	if kind == "style" {
		return "genre"
	}
	return kind
}

func normalize(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func loadRegistry() ([]Concept, map[string]int, map[string]int) {
	var document struct {
		Version  string    `json:"version"`
		Concepts []Concept `json:"concepts"`
	}
	if err := json.Unmarshal(registryJSON, &document); err != nil {
		panic(fmt.Sprintf("musicconcepts: embedded registry: %v", err))
	}
	if document.Version != Version || len(document.Concepts) == 0 {
		panic("musicconcepts: incompatible embedded registry")
	}
	aliases, ids := map[string]int{}, map[string]int{}
	for i, c := range document.Concepts {
		if c.ID == "" || c.Kind == "" || c.Value == "" || c.Source == "" || c.License == "" {
			panic("musicconcepts: incomplete concept")
		}
		if _, exists := ids[c.ID]; exists {
			panic("musicconcepts: duplicate concept identity")
		}
		ids[c.ID] = i
		for _, value := range append([]string{c.Value}, c.Aliases...) {
			key := kindKey(c.Kind) + "\x00" + normalize(value)
			if prior, exists := aliases[key]; exists && prior != i {
				panic("musicconcepts: ambiguous alias within a facet")
			}
			aliases[key] = i
		}
	}
	for _, c := range document.Concepts {
		for _, id := range append(slices.Clone(c.Parents), c.Related...) {
			if _, exists := ids[id]; !exists || id == c.ID {
				panic("musicconcepts: invalid concept relation")
			}
		}
	}
	return document.Concepts, aliases, ids
}

// Canonical translates exact reviewed aliases only. Unknown wording survives
// with whitespace normalized; it is not replaced by a guessed parent concept.
func Canonical(kind, value string) string {
	if c, ok := Find(kind, value); ok {
		return c.Value
	}
	return strings.Join(strings.Fields(value), " ")
}

func Find(kind, value string) (Concept, bool) {
	i, ok := byAlias[kindKey(kind)+"\x00"+normalize(value)]
	if !ok {
		return Concept{}, false
	}
	return clone(entries[i]), true
}

// FindID is useful for a saved, reconciled concept. Callers must still verify
// that its kind/value agrees with the preserved intent rather than trusting an
// arbitrary model-authored ID.
func FindID(id string) (Concept, bool) {
	i, ok := byID[id]
	if !ok {
		return Concept{}, false
	}
	return clone(entries[i]), true
}

func Concepts() []Concept {
	out := make([]Concept, len(entries))
	for i, c := range entries {
		out[i] = clone(c)
	}
	return out
}

func clone(c Concept) Concept {
	c.Aliases = slices.Clone(c.Aliases)
	c.Parents = slices.Clone(c.Parents)
	c.Related = slices.Clone(c.Related)
	c.Providers.MusicBrainz = slices.Clone(c.Providers.MusicBrainz)
	c.Providers.CLAP = slices.Clone(c.Providers.CLAP)
	c.Providers.DSP = slices.Clone(c.Providers.DSP)
	if c.Providers.AcousticBrainz != nil {
		copy := make(map[string]string, len(c.Providers.AcousticBrainz))
		for model, label := range c.Providers.AcousticBrainz {
			copy[model] = label
		}
		c.Providers.AcousticBrainz = copy
	}
	return c
}
