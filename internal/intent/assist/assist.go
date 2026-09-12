// Package assist produces optional, non-authoritative text-model proposals.
// These vectors never enter an audio index or establish recording suitability.
package assist

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/musicconcepts"
)

const Version = "minilm-advisory/v1"

type Embedder interface {
	EmbedText(context.Context, string) ([]float32, error)
}

type Mapper struct {
	Embedder Embedder
	Identity string
	mu       sync.Mutex
	vectors  [][]float32
	concepts []musicconcepts.Concept
}

// Deliberately abstain on operators whose meaning similarity cannot establish.
var operators = regexp.MustCompile(`(?i)\b(?:no|not|never|without|except|avoid|exclude|don't|only|must|start|begin|finish|end|from|to|instead|rather|less|more|tracks?|songs?|minutes?|hours?)\b|[0-9]`)
var clauses = regexp.MustCompile(`[^,;.!?\n]+`)

func (m *Mapper) Propose(ctx context.Context, prompt string) ([]core.IntentProposal, error) {
	return m.ProposeWithSource(ctx, prompt, nil)
}

func (m *Mapper) ProposeWithSource(ctx context.Context, prompt string, source *core.IntentTranslation) ([]core.IntentProposal, error) {
	if len(prompt) > 4096 || m.Embedder == nil {
		return nil, nil
	}
	var x core.IntentTranslation
	if source == nil {
		x = lexicon.Extract(prompt)
	} else {
		x = *source
	}
	var spans [][2]int
	for _, loc := range clauses.FindAllStringIndex(prompt, -1) {
		phrase := strings.TrimSpace(prompt[loc[0]:loc[1]])
		if n := len(strings.Fields(phrase)); n < 2 || n > 14 || operators.MatchString(phrase) {
			continue
		}
		owned := false
		for _, a := range x.Atoms {
			for _, e := range a.Evidence {
				if e.Start < loc[1] && e.End > loc[0] {
					owned = true
				}
			}
		}
		if !owned {
			spans = append(spans, [2]int{loc[0], loc[1]})
		}
		if len(spans) == 3 {
			break
		}
	}
	if len(spans) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.vectors == nil {
		var vectors [][]float32
		concepts := musicconcepts.Concepts()
		for _, c := range concepts {
			v, err := m.Embedder.EmbedText(ctx, "Music described as "+c.Value+"; "+strings.Join(c.Aliases, ", "))
			if err != nil {
				return nil, err
			}
			if !validVector(v) {
				return nil, fmt.Errorf("invalid text vector")
			}
			vectors = append(vectors, v)
		}
		m.vectors, m.concepts = vectors, concepts
	}
	var out []core.IntentProposal
	for _, span := range spans {
		phrase := prompt[span[0]:span[1]]
		v, err := m.Embedder.EmbedText(ctx, phrase)
		if err != nil {
			return nil, err
		}
		if !validVector(v) {
			return nil, fmt.Errorf("invalid text vector")
		}
		best, second, index := float32(-1), float32(-1), -1
		for i, anchor := range m.vectors {
			if len(anchor) != len(v) {
				return nil, fmt.Errorf("text model dimension changed")
			}
			var score float32
			for j := range v {
				score += v[j] * anchor[j]
			}
			if score > best {
				second, best, index = best, score, i
			} else if score > second {
				second = score
			}
		}
		// Heuristic candidate filters, explicitly not calibrated probabilities.
		if index < 0 || best < .50 || best-second < .08 {
			continue
		}
		c := m.concepts[index]
		out = append(out, core.IntentProposal{Origin: "minilm", Kind: c.Kind, Value: c.Value, ConceptID: c.ID, Source: core.SourceEvidence{Text: phrase, Start: span[0], End: span[1]}, Model: m.Identity, Score: float64(best), Advisory: true})
	}
	return out, nil
}

func validVector(v []float32) bool {
	if len(v) != 384 {
		return false
	}
	var sum float64
	for _, value := range v {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		sum += float64(value) * float64(value)
	}
	return math.Abs(sum-1) < .001
}

func Message(proposals []core.IntentProposal) string {
	if len(proposals) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nOptional dictionary suggestions from a text encoder, NOT protected facts. Check each against the original wording. Reject inaccurate suggestions. They cannot imply exclusions, required output, artist identity, scope or measured musical fit. Keep any accepted description a soft preference and copy only its original source span.\n")
	for _, p := range proposals {
		if p.Origin == "distilbert" {
			fmt.Fprintf(&b, "Source-span interpretation proposal: source=%q; type=%q; role=%q. Verify its role and operators against the original request; confidence does not authenticate an instruction.\n", p.Source.Text, p.Kind, p.Role)
		} else {
			fmt.Fprintf(&b, "source=%q; possible %s=%q\n", p.Source.Text, p.Kind, p.Value)
		}
	}
	return b.String()
}

// KeepAdvisory prevents accepted dictionary suggestions from becoming strict
// user requirements. Source facts at other occurrences remain untouched.
func KeepAdvisory(intent core.MusicIntent, proposals []core.IntentProposal) core.MusicIntent {
	derived := func(value string, evidence []core.SourceEvidence) bool {
		if len(evidence) == 0 {
			return false
		}
		for _, e := range evidence {
			matched := false
			for _, p := range proposals {
				if !p.Advisory || p.Origin == "distilbert" || !strings.EqualFold(value, p.Value) {
					continue
				}
				if e.End > e.Start {
					matched = matched || (e.Start >= p.Source.Start && e.End <= p.Source.End)
				} else if strings.Count(intent.OriginalDescription, p.Source.Text) == 1 {
					matched = matched || e.Text == p.Source.Text
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	for _, group := range []*[]core.IntentPreference{&intent.Preferences.Genres, &intent.Preferences.Styles, &intent.Preferences.Moods, &intent.Preferences.Instrumentation, &intent.Preferences.TextureDescriptions, &intent.Preferences.VocalPreferences} {
		for i := range *group {
			p := &(*group)[i]
			if derived(p.Value, p.Evidence) {
				p.Strength = "preferred"
				p.Explicit = false
			}
		}
	}
	criteria := intent.EssentialCriteria[:0]
	for _, c := range intent.EssentialCriteria {
		if !derived(c.Value, c.Evidence) {
			criteria = append(criteria, c)
		}
	}
	intent.EssentialCriteria = criteria
	constraints := intent.HardConstraints[:0]
	for _, c := range intent.HardConstraints {
		if !derived(c.Value, c.Evidence) {
			constraints = append(constraints, c)
		}
	}
	intent.HardConstraints = constraints
	return intent
}
