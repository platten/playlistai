package core

import "strings"

// WantsInstrumental recognizes structured vocal intent, not artist/genre fits.
func WantsInstrumental(intent MusicIntent) bool {
	for _, c := range intent.HardConstraints {
		if c.Kind == "exclude_vocals" || c.Kind == "require_instrumental" {
			return true
		}
	}
	for _, p := range intent.Preferences.VocalRequests() {
		if !instrumentalPreferenceApplies(p) {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(p.Value))
		if p.Influence != InfluenceNegative && (value == "instrumental" || value == "no vocals") ||
			p.Influence == InfluenceNegative && (value == "vocals" || value == "singing") {
			return true
		}
	}
	for _, p := range intent.Preferences.Instrumentation {
		if !instrumentalPreferenceApplies(p) {
			continue
		}
		if p.Influence != InfluenceNegative && strings.EqualFold(strings.TrimSpace(p.Value), "instrumental") {
			return true
		}
	}
	for _, c := range intent.EssentialCriteria {
		if c.Strength != "preferred" && c.Group == "" && (c.Scope == "" || c.Scope == "playlist") && (c.Kind == "vocal" || c.Kind == "instrumentation") && (strings.EqualFold(c.Value, "instrumental") || strings.EqualFold(c.Value, "no vocals")) {
			return true
		}
	}
	return false
}

// VocalRequests uses the plural v9 contract when present, retaining the legacy
// singleton as a read-compatible fallback rather than a duplicate requirement.
func (p SemanticPreferences) VocalRequests() []IntentPreference {
	if len(p.VocalPreferences) > 0 {
		return p.VocalPreferences
	}
	if p.VocalPreference != nil {
		return []IntentPreference{*p.VocalPreference}
	}
	return nil
}

func instrumentalPreferenceApplies(p IntentPreference) bool {
	return p.Strength != "preferred" && p.Degree != "mostly" && p.Degree != "reduced" && p.Group == "" && (p.Scope == "" || p.Scope == "playlist")
}
