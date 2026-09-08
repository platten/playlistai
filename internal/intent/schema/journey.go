package schema

import (
	"strings"

	"github.com/platten/playlistai/internal/intent/rules"
)

// Repair source-grounded category journeys before entity resolution. Adjectives
// describe their stage; genres must not become artist or album destinations.
func preserveCategoryJourney(w *Wire, prompt string) {
	var categories []string
	for _, group := range [][]WirePreference{w.Genres, w.Styles} {
		for _, p := range group {
			if p.Influence != "negative" {
				categories = append(categories, p.Value)
			}
		}
	}
	criteria, trajectory := rules.CategoryJourney(prompt, categories)
	if len(criteria) == 0 {
		return
	}
	for _, group := range []*[]WirePreference{&w.Genres, &w.Styles} {
		for i, p := range *group {
			if p.Influence == "negative" {
				continue
			}
			for _, c := range criteria {
				if strings.EqualFold(p.Value, c.Evidence[0].Text) {
					(*group)[i].Value = c.Value
				}
			}
		}
	}
	isCategory := func(value string) bool {
		for _, c := range criteria {
			if strings.EqualFold(value, c.Value) {
				return true
			}
			for _, e := range c.Evidence {
				if strings.EqualFold(value, e.Text) {
					return true
				}
			}
		}
		return false
	}
	for _, group := range []*[]WireReference{&w.References, &w.JourneyWaypoints, &w.Destination} {
		kept := make([]WireReference, 0, len(*group))
		for _, ref := range *group {
			if ref.Influence == "negative" || !isCategory(ref.Value) {
				kept = append(kept, ref)
			}
		}
		*group = kept
	}
	kept := make([]WireCriterion, 0, len(w.EssentialCriteria))
	for _, c := range w.EssentialCriteria {
		softEnergy := false
		if c.Kind == "mood" && len(trajectory) > 0 {
			for _, stage := range criteria {
				softEnergy = softEnergy || strings.HasPrefix(strings.ToLower(stage.Evidence[0].Text), strings.ToLower(c.Value)+" ")
			}
		}
		if softEnergy {
			w.Moods = append(w.Moods, WirePreference{Value: c.Value, Span: c.Span, Explicit: true, Influence: "positive"})
			continue
		}
		if (c.Kind != "genre" && c.Kind != "style") || !isCategory(c.Value) {
			kept = append(kept, c)
		}
	}
	for _, c := range criteria {
		kept = append(kept, WireCriterion{Kind: "genre", Value: c.Value, Scope: c.Scope, Span: c.Evidence[0].Text})
	}
	w.EssentialCriteria = kept
	w.Mode = "journey"
	if len(trajectory) > 0 {
		w.EnergyTrajectory = nil
		for _, p := range trajectory {
			w.EnergyTrajectory = append(w.EnergyTrajectory, WireEnergy{Position: p.Position, Energy: p.Energy})
		}
	}
}
