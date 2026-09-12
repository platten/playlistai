package lexicon

import (
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var qualifiedIncludePattern = regexp.MustCompile(`(?i)\b(?:must include|include|make sure to include)\s+["“]?([^,;"”\n]+\s+-\s+[^,;"”\n]+)`)
var negatedIncludePrefix = regexp.MustCompile(`(?i)\b(?:not|don't|don’t|never)\s*$`)

// RequiredTracks preserves explicitly included, artist-qualified recordings.
// Unqualified titles and ambiguous include clauses remain for interpretation;
// the extractor never turns a similarity reference into a mandatory recording.
func RequiredTracks(prompt string) []core.IntentReference {
	var out []core.IntentReference
	seen := map[string]bool{}
	for _, r := range requiredTrackOccurrences(prompt) {
		artist, title, _ := core.QualifiedReferenceParts(r.Query)
		key := core.NormalizeIdentityPart(artist) + "\x00" + core.NormalizeIdentityPart(title)
		if !seen[key] {
			out = append(out, r)
			seen[key] = true
		}
	}
	return out
}

func requiredTrackOccurrences(prompt string) []core.IntentReference {
	var out []core.IntentReference
	for _, loc := range qualifiedIncludePattern.FindAllStringSubmatchIndex(prompt, -1) {
		if negatedIncludePrefix.MatchString(prompt[:loc[0]]) {
			continue
		}
		start, end := trimRange(prompt, loc[2], loc[3])
		value := prompt[start:end]
		_, _, ok := core.QualifiedReferenceParts(value)
		if !ok {
			continue
		}
		out = append(out, core.IntentReference{Kind: core.ReferenceTrack, Query: strings.TrimSpace(value), Influence: core.InfluencePositive, Evidence: []core.SourceEvidence{{Text: value, Start: start, End: end, Explicit: true}}})
	}
	return out
}
