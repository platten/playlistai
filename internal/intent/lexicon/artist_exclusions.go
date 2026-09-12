package lexicon

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var leaveArtistOut = regexp.MustCompile(`(?i)\bleave\s+([^,.;!?]+?)\s+out(?:\s+of\s+(?:the\s+)?(?:results|playlist))?\b`)
var eitherArtistRecordings = regexp.MustCompile(`(?i)\b(?:without|excluding|exclude)\s+(?:either|both)\s+(?:band|artist)(?:s['’]?|['’]s)?\s+(?:recordings|tracks|songs)\b`)

// Resolve only a directly adjacent pair of separately named similarity artists.
// More candidates, cross-sentence antecedents and intervening clauses remain
// unresolved rather than guessing which artists a pronoun denotes.
func attachedArtistExclusions(prompt string, atoms []core.IntentAtom) []core.IntentAtom {
	var result []core.IntentAtom
	appendExclusion := func(value string, start, end int) {
		result = append(result, core.IntentAtom{ID: fmt.Sprintf("exclude_artist:%d:%d", start, end), Kind: "exclude_artist", Value: value, Scope: "playlist", Polarity: "negative", Strength: "required", Degree: "plain", Evidence: []core.SourceEvidence{{Text: prompt[start:end], Start: start, End: end, Explicit: true}}})
	}
	for _, p := range leaveArtistOut.FindAllStringSubmatchIndex(prompt, -1) {
		if insideQuoted(prompt, p[0], p[1]) || negatedIncludePrefix.MatchString(prompt[:p[0]]) {
			continue
		}
		value := strings.TrimSpace(prompt[p[2]:p[3]])
		corroborated := nameLike(value) && len(conceptMentions(value)) == 0 && !genericReference(value)
		for _, atom := range atoms {
			corroborated = corroborated || atom.Kind == "artist" && strings.EqualFold(atom.Value, value)
		}
		if corroborated {
			appendExclusion(value, p[0], p[1])
		}
	}
	for _, p := range eitherArtistRecordings.FindAllStringIndex(prompt, -1) {
		if insideQuoted(prompt, p[0], p[1]) || negatedIncludePrefix.MatchString(prompt[:p[0]]) {
			continue
		}
		begin := strings.LastIndexAny(prompt[:p[0]], ".;!?\n") + 1
		var antecedents []core.IntentAtom
		for _, atom := range atoms {
			if atom.Kind == "artist" && atom.Polarity == "positive" && atom.Scope == "playlist" && len(atom.Evidence) == 1 && atom.Evidence[0].Start >= begin && atom.Evidence[0].End <= p[0] {
				antecedents = append(antecedents, atom)
			}
		}
		if len(antecedents) != 2 || strings.EqualFold(antecedents[0].Value, antecedents[1].Value) {
			continue
		}
		first, last := antecedents[0].Evidence[0], antecedents[1].Evidence[0]
		if first.End > last.Start {
			continue
		}
		join := strings.TrimSpace(strings.ToLower(prompt[first.End:last.Start]))
		if join != "and" && join != "," && join != ", and" {
			continue
		}
		if strings.Trim(prompt[last.End:p[0]], " ,\t") != "" {
			continue
		}
		for _, atom := range antecedents {
			appendExclusion(atom.Value, atom.Evidence[0].Start, p[1])
		}
	}
	return result
}
