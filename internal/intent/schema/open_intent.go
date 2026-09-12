package schema

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var centuryPattern = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:st|nd|rd|th)?[\s\p{Pd}]+century\b`)
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
		// An essential texture can also be a requested preference. Preserve both
		// roles; audio.Clauses deduplicates the actual model comparison.
		if !covered {
			w.Textures = append(w.Textures, WirePreference{Value: value, Span: span, Explicit: true, Influence: "positive"})
		}
	}
}

// Unmentioned positive entities are model inventions, not listener instructions.
// Required tracks, exclusions and destinations still require strict validation.
func discardInventedInstructions(w *Wire, prompt string) {
	for _, group := range []*[]WireReference{&w.References, &w.JourneyWaypoints, &w.RequiredTracks, &w.Start, &w.Destination} {
		for i := range *group {
			ref := &(*group)[i]
			// A model may expand a surname into a full artist name. Keep the
			// literal user reference and let catalog/provider evidence resolve
			// identity; model expansion alone is not an authenticated alias.
			if ref.Kind == "artist" && ref.Explicit && containsReferenceWords(prompt, ref.Span) &&
				!containsReferenceWords(prompt, ref.Value) && containsReferenceWords(ref.Value, ref.Span) {
				ref.Value = strings.TrimSpace(ref.Span)
			}
			_, _, qualified := core.QualifiedReferenceParts(ref.Value)
			literalSpan := containsReferenceWords(prompt, ref.Span)
			// Models sometimes copy their normalized artist/title value into
			// the source span. Recover actual source evidence only for that
			// exact rewrite, with both identity parts present in the request.
			rewrittenIdentity := qualified && containsReferenceWords(ref.Value, ref.Span) && containsReferenceWords(ref.Span, ref.Value)
			if ref.Explicit && referenceGrounded(prompt, *ref) && (literalSpan || rewrittenIdentity) && (!literalSpan || !referenceGrounded(ref.Span, *ref)) {
				ref.Span = prompt
			}
		}
	}
	for _, group := range []*[]WireReference{&w.References, &w.JourneyWaypoints} {
		kept := make([]WireReference, 0, len(*group))
		for _, ref := range *group {
			if ref.Influence == "negative" || referenceGrounded(prompt, ref) {
				kept = append(kept, ref)
			}
		}
		*group = kept
	}
	kept := make([]WireConstraint, 0, len(w.HardConstraints))
	for _, constraint := range w.HardConstraints {
		if (constraint.Kind == "journey" || constraint.Kind == "journey_order" || constraint.Kind == "waypoint_order" || constraint.Kind == "transition_order") && len(w.JourneyWaypoints) >= 2 {
			var names []string
			for _, ref := range w.JourneyWaypoints {
				if ref.Influence != "negative" {
					names = append(names, ref.Value)
				}
			}
			ordered := strings.Join(names, " ")
			if len(names) >= 2 && containsReferenceWords(ordered, constraint.Value) && containsReferenceWords(constraint.Value, ordered) {
				continue // same ordered identities are already in the typed journey
			}
		}
		// Output length has exactly one field: total_count. Some models repeat
		// it as a generic constraint; that cannot become an unsupported musical
		// requirement. Literal count repair happens before this normalization;
		// otherwise the validated model control remains authoritative.
		switch constraint.Kind {
		case "count", "track_count", "total_count", "playlist_length":
			continue
		}
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
	decades := decadePattern.FindAllStringSubmatchIndex(prompt, -1)
	if len(mentions)+len(decades) != 1 {
		return
	}
	var match []int
	var first, last int
	basis, scope := "original_release", "playlist"
	if len(mentions) == 1 {
		match = mentions[0]
		n, _ := strconv.Atoi(prompt[match[2]:match[3]])
		if n < 1 {
			return
		}
		first, last = (n-1)*100+1, n*100
		if containsReferenceWords(prompt, "classical") {
			basis = "composition"
		}
	} else {
		match = decades[0]
		first, _ = strconv.Atoi(prompt[match[2]:match[3]])
		last = first + 9
	}
	if negativePeriodPrefix.MatchString(prompt[:match[0]]) {
		return // an excluded period must not become a positive date requirement
	}
	// A year or century inside an explicitly named entity is identity evidence.
	for _, group := range [][]WireReference{w.References, w.RequiredTracks, w.Destination} {
		for _, ref := range group {
			for _, pos := range literalPositions(prompt, ref.Span) {
				if pos[0] <= match[0] && pos[1] >= match[1] {
					return
				}
			}
		}
	}
	if len(w.Temporal) == 1 {
		scope = w.Temporal[0].Scope
		if w.Temporal[0].Basis == "composition" || w.Temporal[0].Basis == "original_release" {
			basis = w.Temporal[0].Basis
		}
	}
	if len(w.Destination) == 1 && strings.Index(strings.ToLower(prompt), strings.ToLower(w.Destination[0].Value)) > match[0] {
		scope = "journey_start"
	}
	w.Temporal = []core.TemporalRequirement{{Basis: basis, StartYear: first, EndYear: last, Scope: scope, Evidence: []core.SourceEvidence{{Text: prompt[match[0]:match[1]], Start: match[0], End: match[1], Explicit: true}}}}
	// The same source period must not also become a sonic texture query.
	text := prompt[match[0]:match[1]]
	kept := w.EssentialCriteria[:0]
	for _, c := range w.EssentialCriteria {
		if c.Kind == "texture" && strings.EqualFold(strings.TrimSpace(c.Value), text) {
			continue
		}
		kept = append(kept, c)
	}
	w.EssentialCriteria = kept
	textures := w.Textures[:0]
	for _, p := range w.Textures {
		if !strings.EqualFold(strings.TrimSpace(p.Value), text) {
			textures = append(textures, p)
		}
	}
	w.Textures = textures
}

// Validate source grounding without deciding which musical words are allowed.
func validateOpenIntent(w Wire, prompt string) error {
	if len(w.Start) > 1 {
		return fmt.Errorf("schema: at most one starting reference")
	}
	if len(w.Destination) > 1 {
		return fmt.Errorf("schema: at most one final destination")
	}
	present := func(span string) bool { return strings.TrimSpace(span) != "" && containsReferenceWords(prompt, span) }
	for _, refs := range [][]WireReference{w.References, w.RequiredTracks, w.JourneyWaypoints, w.Start, w.Destination} {
		for _, ref := range refs {
			if !ref.Explicit || !present(ref.Span) || !referenceGrounded(ref.Span, ref) {
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
