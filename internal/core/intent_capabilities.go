package core

import "strings"

func intentCapabilities() []CapabilityStatus {
	return []CapabilityStatus{
		{Name: "positive_references", Status: "supported", Detail: "independent audio and co-occurrence retrieval for each resolved reference"},
		{Name: "reference_resolution", Status: "supported", Detail: "typed exact, alias, and ranked catalog matching with explicit ambiguity"},
		{Name: "negative_references", Status: "supported", Detail: "resolved references contribute transparent embedding-space ranking penalties"},
		{Name: "required_tracks", Status: "supported", Detail: "resolved catalog tracks"},
		{Name: "hard_artist_exclusions", Status: "supported", Detail: "exact normalized artist identity"},
		{Name: "audio_cooccurrence_weights", Status: "supported", Detail: "independent weights are normalized for blended similarity"},
		{Name: "total_track_count", Status: "supported", Detail: "total output length including required tracks"},
		{Name: "essential_musical_criteria", Status: "unsupported", Detail: "requires affirmative grounded feature or compatible semantic evidence"},
		{Name: "semantic_preferences", Status: "unsupported", Detail: "preserved; active only when a compatible grounded semantic sidecar is loaded"},
		{Name: "discovery", Status: "supported", Detail: "seeded bounded exploration among sufficiently relevant exact-search candidates"},
		{Name: "artist_diversity", Status: "supported", Detail: "controls transparent MMR concentration penalties and soft artist spacing"},
		{Name: "transition_smoothness", Status: "supported", Detail: "controls greedy embedding transitions with bounded local improvement"},
		{Name: "energy_trajectory", Status: "unsupported", Detail: "preserved but catalog has no energy feature"},
	}
}

// HardConstraintSupported is the canonical enforcement registry used by all
// parsers and migrations. Unknown kinds are always preservation-only.
func HardConstraintSupported(kind string) bool {
	switch kind {
	case "exclude_artist", "exclude_reference_artists", "no_back_to_back_artist":
		return true
	default:
		return false
	}
}

func addUnsupportedConstraints(out []UnsupportedRequirement, constraints []HardConstraint) []UnsupportedRequirement {
	for _, constraint := range constraints {
		if constraint.Supported {
			continue
		}
		text := constraint.Value
		if len(constraint.Evidence) > 0 && constraint.Evidence[0].Text != "" {
			text = constraint.Evidence[0].Text
		}
		found := false
		for _, existing := range out {
			if strings.EqualFold(existing.Text, text) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, UnsupportedRequirement{
				Text: text, Reason: "the current catalog cannot enforce " + constraint.Kind,
				Evidence: constraint.Evidence,
			})
		}
	}
	return out
}
