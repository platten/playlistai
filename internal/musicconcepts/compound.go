package musicconcepts

import "strings"

// CompoundGenreLeads returns reviewed, two-part discovery phrases for a
// compound genre. The parts are retrieval/corroboration leads, never proof of
// the complete named subgenre. Unreviewed adjective splitting is excluded.
func CompoundGenreLeads(kind, value string) [][]string {
	if kind != "genre" && kind != "style" {
		return nil
	}
	concept, ok := Find(kind, value)
	if !ok || len(concept.Providers.MusicBrainzDiscovery) == 0 {
		return nil
	}
	var out [][]string
	seen := map[string]bool{}
	for _, phrase := range concept.Providers.MusicBrainzDiscovery {
		parts := strings.Fields(phrase)
		if len(parts) != 2 {
			continue
		}
		first, firstOK := Find("genre", parts[0])
		second, secondOK := Find("genre", parts[1])
		if !firstOK || !secondOK || first.ID == second.ID {
			continue
		}
		key := first.ID + ":" + second.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, []string{first.Value, second.Value})
	}
	return out
}
