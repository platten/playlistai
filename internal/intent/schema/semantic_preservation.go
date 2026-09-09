package schema

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

// Naming an artist is identity evidence, not a user instruction to enforce a
// genre the model associates with that artist. Keep actual descriptive spans.
func discardReferenceAttributedDescriptions(w *Wire) {
	attributed := func(value, span string) bool {
		for _, group := range [][]WireReference{w.References, w.JourneyWaypoints} {
			for _, ref := range group {
				if ref.Kind == "artist" && ref.Explicit && containsReferenceWords(span, ref.Value) && containsReferenceWords(ref.Value, span) &&
					(!containsReferenceWords(value, ref.Value) || !containsReferenceWords(ref.Value, value)) {
					return true
				}
			}
		}
		return false
	}
	criteria := make([]WireCriterion, 0, len(w.EssentialCriteria))
	for _, c := range w.EssentialCriteria {
		if !attributed(c.Value, c.Span) {
			criteria = append(criteria, c)
		}
	}
	w.EssentialCriteria = criteria
	for _, group := range []*[]WirePreference{&w.Genres, &w.Styles, &w.Moods, &w.Instrumentation, &w.Textures} {
		kept := make([]WirePreference, 0, len(*group))
		for _, p := range *group {
			if !attributed(p.Value, p.Span) {
				kept = append(kept, p)
			}
		}
		*group = kept
	}
	if attributed(w.VocalPreference.Value, w.VocalPreference.Span) {
		w.VocalPreference = WirePreference{}
	}
}

// The open-vocabulary model remains the main interpreter. The conservative
// fallback can corroborate a defining category it omitted; it is not a list
// restricting which genres the model is allowed to understand.
func preserveDefiningCategory(w *Wire, prompt string) {
	expected, _ := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	for _, c := range expected.EssentialCriteria {
		if c.Scope != "playlist" || (c.Kind != "style" && c.Kind != "genre") || len(c.Evidence) == 0 {
			continue // category journeys have their own source-aware repair
		}
		span := c.Evidence[0]
		// A partial match inside a compound genre (e.g. rock in post-rock)
		// must not substitute its parent for the requested category.
		if span.Start > 0 && prompt[span.Start-1] == '-' || span.End >= 0 && span.End < len(prompt) && prompt[span.End] == '-' {
			continue
		}
		genreCovered := false
		for _, p := range w.Genres {
			genreCovered = genreCovered || p.Influence != "negative" && containsReferenceWords(p.Value, c.Value)
		}
		essentialCovered := false
		for _, existing := range w.EssentialCriteria {
			essentialCovered = essentialCovered || (existing.Kind == "genre" || existing.Kind == "style") && containsReferenceWords(existing.Value, c.Value)
		}
		if essentialCovered {
			continue // retain the existing scope/subgenre instead of adding a parent stage
		}
		if !genreCovered {
			w.Genres = append(w.Genres, WirePreference{Value: c.Value, Span: span.Text, Influence: "positive", Explicit: true})
		}
		w.EssentialCriteria = append(w.EssentialCriteria, WireCriterion{Kind: "genre", Value: c.Value, Scope: "playlist", Span: span.Text})
	}
}

// Influence carries the negation; the encoder should compare against "sleepy",
// not subtract similarity to "not sleepy". Restrict this repair to an exact
// copied negative span, preserving genre names such as an excluded "no wave"
// whose source is "no no wave".
func normalizeNegativeDescriptions(w *Wire) {
	for _, group := range []*[]WirePreference{&w.Genres, &w.Styles, &w.Moods, &w.Instrumentation, &w.Textures} {
		for i := range *group {
			p := &(*group)[i]
			if p.Influence != "negative" || !p.Explicit || !strings.EqualFold(strings.TrimSpace(p.Value), strings.TrimSpace(p.Span)) {
				continue
			}
			for _, prefix := range []string{"not ", "no ", "without "} {
				if strings.HasPrefix(strings.ToLower(p.Value), prefix) {
					p.Value = strings.TrimSpace(p.Value[len(prefix):])
					break
				}
			}
		}
	}
}
