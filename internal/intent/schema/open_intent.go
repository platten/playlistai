package schema

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var centuryPattern = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:st|nd|rd|th)?\s+century\b`)
var qualityClausePattern = regexp.MustCompile(`(?i)\bwith\s+((?:(?:lots|plenty|a lot)\s+of|(?:a|an)\s+(?:good|strong|rich|delicate))\s+(.+?))(?:\s+(?:transitioning|leading|ending|moving|and then)\b|[,;.]|$)`)

// Preserve an omitted open-vocabulary quality clause without inferring any
// artists or claiming a measurable property. The local model remains responsible
// for more specific classification; this fallback keeps its source wording.
func preserveQualityClauses(w *Wire, prompt string) {
	for _, match := range qualityClausePattern.FindAllStringSubmatch(prompt, -1) {
		value, span := strings.TrimSpace(match[2]), strings.TrimSpace(match[1])
		covered := false
		for _, prefs := range [][]WirePreference{w.Genres, w.Styles, w.Moods, w.Textures, w.Instrumentation, {w.VocalPreference}} {
			for _, p := range prefs {
				covered = covered || p.Value != "" && (containsReferenceWords(p.Span, value) || containsReferenceWords(p.Value, value))
			}
		}
		for _, c := range w.EssentialCriteria {
			covered = covered || containsReferenceWords(c.Span, value) || containsReferenceWords(c.Value, value)
		}
		if !covered {
			w.Textures = append(w.Textures, WirePreference{Value: value, Span: span, Explicit: true, Influence: "positive"})
		}
	}
}

// Unmentioned positive entities are model inventions, not listener instructions.
// Required tracks, exclusions and destinations still require strict validation.
func discardInventedInstructions(w *Wire, prompt string) {
	for _, group := range []*[]WireReference{&w.References, &w.JourneyWaypoints} {
		kept := make([]WireReference, 0, len(*group))
		for _, ref := range *group {
			if ref.Influence == "negative" || containsReferenceWords(prompt, ref.Value) {
				kept = append(kept, ref)
			}
		}
		*group = kept
	}
	kept := make([]WireConstraint, 0, len(w.HardConstraints))
	for _, constraint := range w.HardConstraints {
		if constraint.Kind == "energy_trajectory" && !containsReferenceWords(prompt, "energy") && !containsReferenceWords(prompt, "intensity") {
			continue
		}
		kept = append(kept, constraint)
	}
	w.HardConstraints = kept
}

// Calendar arithmetic and the selected classical-era convention are stable
// semantics, independent of any genre-to-recording recommendation mapping.
func normalizePeriods(w *Wire, prompt string) {
	mentions := centuryPattern.FindAllStringSubmatchIndex(prompt, -1)
	if len(mentions) != 1 || len(w.Temporal) > 1 {
		return
	}
	match := mentions[0]
	n, _ := strconv.Atoi(prompt[match[2]:match[3]])
	if n < 1 {
		return
	}
	basis, scope := "original_release", "playlist"
	if containsReferenceWords(prompt, "classical") {
		basis = "composition"
	}
	if len(w.Temporal) == 1 {
		scope = w.Temporal[0].Scope
	}
	if len(w.Destination) == 1 && strings.Index(strings.ToLower(prompt), strings.ToLower(w.Destination[0].Value)) > match[0] {
		scope = "journey_start"
	}
	w.Temporal = []core.TemporalRequirement{{Basis: basis, StartYear: (n-1)*100 + 1, EndYear: n * 100, Scope: scope, Evidence: []core.SourceEvidence{{Text: prompt[match[0]:match[1]], Start: match[0], End: match[1], Explicit: true}}}}
}

// Validate source grounding without deciding which musical words are allowed.
func validateOpenIntent(w Wire, prompt string) error {
	if len(w.Destination) > 1 {
		return fmt.Errorf("schema: at most one final destination")
	}
	present := func(span string) bool { return strings.TrimSpace(span) != "" && containsReferenceWords(prompt, span) }
	for _, refs := range [][]WireReference{w.References, w.RequiredTracks, w.JourneyWaypoints, w.Destination} {
		for _, ref := range refs {
			if !ref.Explicit || !present(ref.Span) || !containsReferenceWords(ref.Span, ref.Value) {
				return fmt.Errorf("schema: ungrounded explicit %s reference %q", ref.Kind, ref.Value)
			}
			if ref.Influence == "negative" {
				found := false
				for _, c := range w.HardConstraints {
					if c.Kind == "exclude_"+ref.Kind && core.NormalizeIdentityPart(c.Value) == core.NormalizeIdentityPart(ref.Value) {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("schema: negative entity reference needs an exclusion")
				}
			}
		}
	}
	for _, prefs := range [][]WirePreference{w.Genres, w.Styles, w.Moods, w.Instrumentation, w.Textures, {w.VocalPreference}} {
		for _, p := range prefs {
			if p.Value != "" && p.Explicit && !present(p.Span) {
				return fmt.Errorf("schema: ungrounded preference %q", p.Value)
			}
		}
	}
	for _, c := range w.EssentialCriteria {
		if !present(c.Span) {
			return fmt.Errorf("schema: ungrounded defining criterion %q", c.Value)
		}
	}
	for _, c := range w.HardConstraints {
		if !present(c.Span) {
			return fmt.Errorf("schema: ungrounded constraint %q", c.Value)
		}
		if c.Kind == "energy_trajectory" && !containsReferenceWords(prompt, "energy") && !containsReferenceWords(prompt, "intensity") {
			return fmt.Errorf("schema: a transition between references is not a strict energy trajectory; remove that invented constraint")
		}
	}
	// A category remains a category even if a similarly named artist exists.
	for _, g := range w.Genres {
		for _, ref := range w.References {
			if ref.Kind == "artist" && core.NormalizeIdentityPart(ref.Value) == core.NormalizeIdentityPart(g.Value) && ref.Span == g.Span {
				return fmt.Errorf("schema: same mention classified as genre and artist")
			}
		}
	}
	return nil
}
