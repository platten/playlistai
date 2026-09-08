package rules

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// CategoryJourney separates category names from ordinary energy adjectives.
// Additional categories come from the model's open-vocabulary interpretation;
// they are not a whitelist of supported genres or a mapping to musical fits.
func CategoryJourney(prompt string, categories []string) ([]core.MusicalCriterion, []core.EnergyPoint) {
	match := reJourney.FindStringSubmatch(prompt)
	if match == nil {
		return nil, nil
	}
	parts := []string{match[1], match[2]}
	scopes := []string{"journey_start", "journey_end"}
	if match[3] != "" {
		parts = append(parts, match[3])
		scopes = append(scopes, "journey_via")
	}
	var criteria []core.MusicalCriterion
	energy := make([]float64, len(parts))
	for i := range energy {
		energy[i] = .5
	}
	energyMentioned := false
	for i, part := range parts {
		value := strings.ToLower(cleanSeed(part))
		explicitCategory := strings.HasPrefix(value, "genre ") || strings.HasPrefix(value, "style ") || strings.HasSuffix(value, " genre")
		value = strings.TrimPrefix(strings.TrimPrefix(value, "genre "), "style ")
		value = strings.TrimSuffix(value, " genre")
		words := strings.Fields(value)
		for len(words) > 1 {
			level, ok := journeyEnergy(words[0])
			if !ok {
				break
			}
			energy[i] = level
			energyMentioned = true
			words = words[1:]
		}
		value = strings.Join(words, " ")
		known := explicitCategory || isKnownStyle(value)
		for _, category := range categories {
			categoryWords := strings.Fields(strings.ToLower(cleanSeed(category)))
			for len(categoryWords) > 1 {
				if _, ok := journeyEnergy(categoryWords[0]); !ok {
					break
				}
				categoryWords = categoryWords[1:]
			}
			known = known || strings.EqualFold(strings.Join(categoryWords, " "), value)
		}
		if !known || value == "" {
			return nil, nil
		} // retain entity journeys for normal identity resolution
		criteria = append(criteria, core.MusicalCriterion{Kind: "style", Value: normalizeStyle(value), Scope: scopes[i], Evidence: sourceEvidence(prompt, strings.TrimSpace(part), true)})
	}
	var trajectory []core.EnergyPoint
	if energyMentioned {
		trajectory = []core.EnergyPoint{{Position: 0, Energy: energy[0]}}
		if len(parts) > 2 {
			trajectory = append(trajectory, core.EnergyPoint{Position: .5, Energy: energy[2]})
		}
		trajectory = append(trajectory, core.EnergyPoint{Position: 1, Energy: energy[1]})
	}
	return criteria, trajectory
}

func journeyEnergy(word string) (float64, bool) {
	switch word {
	case "energetic", "upbeat", "intense", "high-energy":
		return .8, true
	case "calm", "gentle", "mellow", "relaxing", "low-energy":
		return .2, true
	default:
		return 0, false
	}
}
