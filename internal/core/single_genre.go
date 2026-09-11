package core

// SinglePlaylistGenre identifies a single category applying to every track.
// Journey categories and deliberately mixed categories must not become a
// playlist-wide intersection. Mood/texture adjectives are not genre filters.
func SinglePlaylistGenre(intent MusicIntent) (MusicalCriterion, bool) {
	var result MusicalCriterion
	values := map[string]bool{}
	add := func(c MusicalCriterion) {
		key := CanonicalStyle(c.Value)
		if key == "" {
			return
		}
		values[key] = true
		if c.Scope == "" || c.Scope == "playlist" {
			result = c
			result.Scope = "playlist"
		}
	}
	for _, c := range intent.EssentialCriteria {
		if c.Kind == "genre" || c.Kind == "style" {
			if c.Scope != "" && c.Scope != "playlist" {
				return MusicalCriterion{}, false
			}
			add(c)
		}
	}
	for _, p := range intent.Preferences.Genres {
		if p.Influence != InfluenceNegative {
			add(MusicalCriterion{Kind: "genre", Value: p.Value, Scope: "playlist", Evidence: p.Evidence})
		}
	}
	return result, len(values) == 1 && result.Value != ""
}
