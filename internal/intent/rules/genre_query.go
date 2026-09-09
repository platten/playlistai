package rules

import (
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var explicitReferenceSyntax = regexp.MustCompile(`(?i)\b(?:like|by|artist|track|album|from|to|with|without|no|not|except|include|including|similar|inspired)\b|["“”]`)

// BareGenreQuery extracts a possible category, not proof that it is a genre.
// Callers must corroborate it with provider genre data before reclassifying a
// bare reference. This keeps arbitrary genres supported without a name list.
func BareGenreQuery(prompt string) string {
	text := strings.Trim(strings.TrimSpace(maskTrackCounts(prompt)), ",;.!? ")
	text = strings.TrimSpace(reLeadVerb.ReplaceAllString(text, ""))
	text = strings.Join(strings.Fields(text), " ")
	for _, article := range []string{"a ", "an "} {
		if strings.HasPrefix(strings.ToLower(text), article) {
			text = strings.TrimSpace(text[len(article):])
			break
		}
	}
	if text == "" || explicitReferenceSyntax.MatchString(text) || strings.ContainsAny(text, ",;") {
		return ""
	}
	for _, suffix := range []string{" music", " playlist", " songs", " tracks", " mix"} {
		if strings.HasSuffix(strings.ToLower(text), suffix) {
			text = strings.TrimSpace(text[:len(text)-len(suffix)])
			break
		}
	}
	return text
}

// ApplyConfirmedGenre is only for a provider-confirmed bare category. Counts
// and source offsets stay tied to the original request, not the lookup string.
func ApplyConfirmedGenre(intent core.MusicIntent, name string) core.MusicIntent {
	start := strings.Index(intent.OriginalDescription, name)
	if start < 0 {
		return intent
	}
	evidence := []core.SourceEvidence{{Text: name, Explicit: true, Start: start, End: start + len(name)}}
	intent.References = nil
	intent.Seeds.Queries = nil
	intent.Seeds.TrackIDs = nil
	intent.Preferences.Genres = []core.IntentPreference{{Value: name, Influence: core.InfluencePositive, Explicit: true, Evidence: evidence}}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: name, Scope: "playlist", Evidence: evidence}}
	return intent
}
