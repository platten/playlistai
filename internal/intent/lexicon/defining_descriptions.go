package lexicon

import (
	"regexp"
	"strings"
)

// Preserve the entire requested sound. These are source phrases, not claims
// that a recording has that sound or aliases for a generic instrument tag.
var definingInstrument = regexp.MustCompile(`(?i)\b(?:soft|gentle|delicate|pounding|thundering|distorted|fuzzy|overdriven|reverberant|echoing|ringing|muted|crunchy|shimmering|jangly|warm|intimate|dark|tense|sweeping|orchestral)(?:\s+(?:warm|intimate|dark|tense|sweeping|orchestral|shimmering|delicate|soft|gentle))*\s+(?:acoustic\s+|electric\s+)?(?:pianos?|drums?|guitars?|percussion|strings|synths?|bass|vocals|atmosphere|arrangements)\b`)
var definingTexture = regexp.MustCompile(`(?i)\b(?:spacious|lush|deep|heavy|long|subtle)\s+(?:reverberation|reverb|distortion|echo)\b`)

var optionalDescriptionSuffix = regexp.MustCompile(`(?i)^\s*,?\s*(?:if possible|if available|preferably|optionally)\s*(?:$|[.;!?]|,|and\b|or\b|but\b)`)
var definingCoordination = regexp.MustCompile(`(?i)^\s*,?\s+(?:and|or|nor)\s+`)

// Literal compound negatives must be recognized before logical grouping. A
// vocal quality owns its whole phrase rather than excluding every warm sound.
func coordinatedDefiningNegative(prompt string, start, end int) bool {
	return definingCoordination.MatchString(prompt[end:]) || strings.HasSuffix(strings.ToLower(prompt[start:end]), "vocals") || optionalDescriptionSuffix.MatchString(prompt[end:])
}

// Musical nouns bound an open description. Qualifying words remain literal:
// recognizing the phrase does not assert a parent feature or infer its sound.
var literalMusicalNoun = regexp.MustCompile(`(?i)\b(?:bass lines?|instruments?|rhythms?|feel|textures?|grooves?|contrasts|atmosphere|pulse|energy|bass|percussion)\b`)
var literalMusicalWord = regexp.MustCompile(`[\p{L}\p{M}]+(?:-[\p{L}\p{M}]+)*$`)

func literalDescriptionMentions(prompt string) []mention {
	var out []mention
	for _, noun := range literalMusicalNoun.FindAllStringIndex(prompt, -1) {
		start := noun[0]
	words:
		for count := 0; count < 4; count++ {
			prefix := strings.TrimRight(prompt[:start], " \t")
			word := literalMusicalWord.FindStringIndex(prefix)
			if word == nil {
				break
			}
			value := strings.ToLower(prefix[word[0]:word[1]])
			switch value {
			case "of", "with", "a", "an", "the", "and", "or", "but", "no", "not", "without", "neither", "nor", "avoid", "optional", "optionally", "preferably", "from", "through", "to", "into", "include", "including", "music", "playlist", "song", "songs", "track", "tracks", "make", "give", "me", "have", "has", "that", "start", "starts", "build", "finish", "finishes", "in", "for", "featuring", "by", "like", "prefer", "less", "more", "must", "only", "mostly", "then", "keep", "some", "occasional", "occasionally", "mainly", "ideally", "possible", "available", "i", "you", "we", "it", "they", "he", "she", "my", "your", "our", "their", "its", "love", "want", "need", "please", "choose", "find", "play", "add", "bring", "use":
				break words
			}
			start = word[0]
		}
		if start == noun[0] {
			continue
		}
		kind := "texture"
		nounText := strings.ToLower(prompt[noun[0]:noun[1]])
		if nounText == "bass" || nounText == "percussion" || strings.HasPrefix(nounText, "bass line") || strings.HasPrefix(nounText, "instrument") {
			kind = "instrumentation"
		}
		out = append(out, mention{start: start, end: noun[1], kind: kind, value: strings.ToLower(prompt[start:noun[1]])})
	}
	return out
}
