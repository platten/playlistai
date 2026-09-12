package musicbrainz

import (
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// discoveryGenres uses approved spelling variants as retrieval queries. Neither
// an artist tag nor a parent genre is proof that a recording fits the request.
// Negative preferences never seed retrieval; downstream exclusions still apply.
func discoveryGenres(intent core.MusicIntent) []string {
	var genres []string
	var concepts [][]string
	seen := map[string]bool{}
	add := func(kind, value string) {
		queries := []string{value}
		if c, ok := musicconcepts.Find(kind, value); ok {
			queries = append([]string{c.Value}, c.Providers.MusicBrainz...)
		}
		concepts = append(concepts, queries)
	}
	appendQuery := func(query string) {
		key := core.NormalizeIdentityPart(query)
		if key != "" && !seen[key] {
			genres = append(genres, query)
			seen[key] = true
		}
	}
	for _, preferences := range [][]core.IntentPreference{intent.Preferences.Genres, intent.Preferences.Styles} {
		for _, p := range preferences {
			if p.Influence != core.InfluenceNegative {
				add("genre", p.Value)
			}
		}
	}
	for _, c := range intent.EssentialCriteria {
		if c.Kind == "genre" || c.Kind == "style" {
			add(c.Kind, c.Value)
		}
	}
	// Give each requested category its first query before spelling expansions
	// spend the shared provider budget, including in multi-stage journeys.
	for round := 0; ; round++ {
		more := false
		for _, queries := range concepts {
			if round < len(queries) {
				appendQuery(queries[round])
				more = true
			}
		}
		if !more {
			break
		}
	}
	return genres
}
