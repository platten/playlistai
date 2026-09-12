package catalog

import (
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// resolveArtistTypo is deliberately conservative: at least two complete name
// tokens, one edit at most across the entire name, and a unique catalog entity.
// It never drops words or returns a track whose title happens to match an artist.
func (c *Catalog) resolveArtistTypo(query string) core.ReferenceResolution {
	q := strings.Fields(normalizeUnicodeSearch(query))
	if len(q) < 2 || len(q) > 5 || len(query) > 160 {
		return unresolved()
	}
	for _, token := range q {
		if len([]rune(token)) < 3 {
			return unresolved()
		}
	}
	var names []string
	if c.artistRows != nil {
		for artist := range c.artistRows {
			names = append(names, artist)
		}
	} else {
		rows, err := c.db.Query("SELECT DISTINCT artist FROM tracks ORDER BY artist LIMIT 20001")
		if err != nil {
			return unresolved()
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) != nil {
				return unresolved()
			}
			names = append(names, name)
		}
		if rows.Err() != nil || len(names) > 20000 {
			return unresolved()
		}
	}
	// Normalize-equivalent spellings must choose the same representative before
	// duplicate collapse, irrespective of map iteration order.
	sort.Strings(names)
	var candidates []core.ResolutionCandidate
	seen := map[string]bool{}
	for _, artist := range names {
		key := normalizeUnicodeSearch(artist)
		if seen[key] {
			continue
		}
		// Umlaut transliteration is an alternate spelling, not a new identity.
		german := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(artist))
		for _, variant := range []string{key, normalizeUnicodeSearch(german)} {
			words := strings.Fields(variant)
			if len(words) != len(q) {
				continue
			}
			edits := 0
			for i := range q {
				edits += boundedNameDistance(q[i], words[i])
			}
			if edits <= 1 {
				seen[key] = true
				candidates = append(candidates, core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: "artist:" + key, Artist: artist, Confidence: .9,
					Evidence: []core.ResolutionEvidence{{Match: "spelling", NormalizedQuery: normalizeUnicodeSearch(query), MatchedText: artist}}})
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].EntityID < candidates[j].EntityID })
	if len(candidates) == 0 {
		return unresolved()
	}
	if len(candidates) > maxAlternatives {
		candidates = candidates[:maxAlternatives]
	}
	for i := range candidates {
		candidates[i].Representatives = c.artistRepresentatives(candidates[i].Artist)
	}
	if len(candidates) > 1 {
		return core.ReferenceResolution{Status: core.ResolutionAmbiguous, Alternatives: candidates}
	}
	return resolved(candidates[0])
}

// Returns 2 once two edits are necessary. Unicode runes keep a name typo from
// being mistaken for several byte edits. Different initials are not corrected.
func boundedNameDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	if len(x) == 0 || len(y) == 0 || x[0] != y[0] || len(x)-len(y) > 1 || len(y)-len(x) > 1 {
		return 2
	}
	i, j, edits := 0, 0, 0
	for i < len(x) && j < len(y) {
		if x[i] == y[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return 2
		}
		if len(x) > len(y) {
			i++
		} else if len(y) > len(x) {
			j++
		} else {
			i++
			j++
		}
	}
	if i < len(x) || j < len(y) {
		edits++
	}
	return edits
}
