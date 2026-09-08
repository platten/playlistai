package core

import "strings"

// QualifiedReferenceParts separates conventional artist/title forms without
// dropping title words or guessing an artist from a musical description.
// The returned values are artist, title; ok is false for an unqualified title.
func QualifiedReferenceParts(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	var artist, title string
	if i := strings.LastIndex(strings.ToLower(value), " by "); i > 0 {
		title, artist = value[:i], value[i+4:]
	} else {
		for _, separator := range []string{" - ", " — ", " – ", "'s ", "’s "} {
			if i := strings.Index(value, separator); i > 0 {
				artist, title = value[:i], value[i+len(separator):]
				break
			}
		}
	}
	artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
	return artist, title, artist != "" && title != ""
}
