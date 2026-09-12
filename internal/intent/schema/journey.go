package schema

import (
	"strings"

	"github.com/platten/playlistai/internal/intent/rules"
)

// A description attached to the first named artist must not be copied onto
// every stage. Repair only unique source spans between ordered artist names;
// repeated or ambiguous wording stays with the model's interpretation.
func preserveArtistStageDescriptions(w *Wire, prompt string) {
	if len(w.JourneyWaypoints) < 2 {
		return
	}
	var positions [][]int
	for _, ref := range w.JourneyWaypoints {
		if ref.Kind != "artist" || !ref.Explicit || ref.Influence == "negative" {
			return
		}
		matches := literalPositions(prompt, ref.Value)
		if len(matches) != 1 {
			matches = literalPositions(prompt, ref.Span)
		}
		if len(matches) != 1 || len(positions) > 0 && matches[0][0] < positions[len(positions)-1][1] {
			return
		}
		positions = append(positions, matches[0])
	}
	for i, c := range w.EssentialCriteria {
		if !strings.HasPrefix(c.Scope, "journey_") {
			continue
		}
		matches := literalPositions(prompt, c.Span)
		if len(matches) != 1 {
			continue
		}
		for stage, pos := range positions {
			end := len(prompt)
			if stage+1 < len(positions) {
				end = positions[stage+1][0]
			}
			if matches[0][0] >= pos[1] && matches[0][1] <= end {
				scope := "journey_via"
				if stage == 0 {
					scope = "journey_start"
				} else if stage == len(positions)-1 {
					scope = "journey_end"
				}
				w.EssentialCriteria[i].Scope = scope
				break
			}
		}
	}
}

// Repair source-grounded category journeys before entity resolution. Adjectives
// describe their stage; genres must not become artist or album destinations.
func preserveCategoryJourney(w *Wire, prompt string) {
	// A model can emit correctly scoped category stages but leave mode at
	// "similar". Keep the execution mode consistent with that contract;
	// otherwise retrieval/ordering silently flatten the requested journey.
	defer func() {
		start, end := false, false
		for _, c := range w.EssentialCriteria {
			if c.Kind == "genre" || c.Kind == "style" {
				start = start || c.Scope == "journey_start"
				end = end || c.Scope == "journey_end"
			}
		}
		positiveWaypoints := 0
		for _, ref := range w.JourneyWaypoints {
			if ref.Influence != "negative" {
				positiveWaypoints++
			}
		}
		if start && end || positiveWaypoints >= 2 {
			w.Mode = "journey"
		}
	}()
	var categories []string
	for _, group := range [][]WirePreference{w.Genres, w.Styles} {
		for _, p := range group {
			if p.Influence != "negative" {
				categories = append(categories, p.Value)
			}
		}
	}
	for _, c := range w.EssentialCriteria {
		if c.Kind == "genre" || c.Kind == "style" {
			categories = append(categories, c.Value)
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
