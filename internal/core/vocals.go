package core

import "strings"

// WantsInstrumental recognizes structured vocal intent, not artist/genre fits.
func WantsInstrumental(intent MusicIntent) bool {
	for _, c := range intent.HardConstraints {
		if c.Kind == "exclude_vocals" || c.Kind == "require_instrumental" {
			return true
		}
	}
	if p := intent.Preferences.VocalPreference; p != nil {
		value := strings.ToLower(strings.TrimSpace(p.Value))
		if p.Influence != InfluenceNegative && (value == "instrumental" || value == "no vocals") ||
			p.Influence == InfluenceNegative && (value == "vocals" || value == "singing") {
			return true
		}
	}
	for _, p := range intent.Preferences.Instrumentation {
		if p.Influence != InfluenceNegative && strings.EqualFold(strings.TrimSpace(p.Value), "instrumental") {
			return true
		}
	}
	for _, c := range intent.EssentialCriteria {
		if (c.Scope == "" || c.Scope == "playlist") && (c.Kind == "vocal" || c.Kind == "instrumentation") && (strings.EqualFold(c.Value, "instrumental") || strings.EqualFold(c.Value, "no vocals")) {
			return true
		}
	}
	return false
}
