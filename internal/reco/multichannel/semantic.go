package multichannel

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
)

func semanticQueryText(intent core.MusicIntent) (string, string) {
	var positive, negative []string
	add := func(preferences []core.IntentPreference) {
		for _, preference := range preferences {
			if preference.Influence == core.InfluenceNegative {
				negative = appendUniqueString(negative, preference.Value)
			} else {
				positive = appendUniqueString(positive, preference.Value)
			}
		}
	}
	add(intent.Preferences.Genres)
	add(intent.Preferences.Styles)
	add(intent.Preferences.Moods)
	add(intent.Preferences.Instrumentation)
	add(intent.Preferences.TextureDescriptions)
	for _, criterion := range intent.EssentialCriteria {
		positive = appendUniqueString(positive, criterion.Value)
	}
	add(intent.Preferences.VocalRequests())
	return strings.Join(positive, ". "), strings.Join(negative, ". ")
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
