package rules

import (
	"regexp"
	"strings"
)

var onlyArtistPattern = regexp.MustCompile(`(?i)\b(?:songs|tracks|music|playlist)\s+(?:only|exclusively)\s+by\s+([^,.;!?]+)`)
var explicitArtistSpacing = regexp.MustCompile(`(?i)\b(?:(?:no|avoid)\s+back[- ]to[- ]back\s+artists?|(?:no|avoid|do not|don't|never)\s+(?:placing\s+)?(?:the\s+)?same\s+artist\s+(?:back[- ]to[- ]back|consecutively)|(?:no|do not|don't|never)\s+repeat\s+artists?)\b`)

// OnlyArtist recognizes an explicit, unambiguous artist-only instruction.
// Ordinary "like Artist" requests must never acquire this restriction.
func OnlyArtist(prompt string) string {
	match := onlyArtistPattern.FindStringSubmatch(maskTrackCounts(prompt))
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
