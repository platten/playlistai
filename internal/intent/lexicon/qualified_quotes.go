package lexicon

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	qualifiedBy         = regexp.MustCompile(`(?i)^\s+by\s+`)
	quotedJoin          = regexp.MustCompile(`(?i)^\s*(?:,\s*(?:and\s+)?|and\s+)`)
	quotedJoinSuffix    = regexp.MustCompile(`(?i)(?:,\s*(?:and\s+)?|\s+and\s+)$`)
	possessiveSound     = regexp.MustCompile(`(?i)^(.+?)['’]s\s+.+\b(?:sound|style|atmosphere|music)\b`)
	viaMarker           = regexp.MustCompile(`(?i)\b(?:moves?|moving|passes?|passing)\s+through\s+`)
	otherArtistsPattern = regexp.MustCompile(`(?i)\b(?:include|including|with|using)\s+(?:some\s+)?other artists\b`)
)

type quotedReference struct {
	start, end                   int
	kind, value, scope, strength string
}

// quotedReferenceMentions protects a whole quoted artist name, or a qualified
// title, before any conjunction splitting. A title's artist remains intact
// (including "and") until another explicitly quoted reference begins.
func quotedReferenceMentions(prompt string) []quotedReference {
	var out []quotedReference
	for _, loc := range referenceIntro.FindAllStringIndex(prompt, -1) {
		if insideQuoted(prompt, loc[0], loc[1]) || overlapsQuotedReference(out, loc[0], loc[1]) {
			continue
		}
		intro := strings.ToLower(strings.TrimSpace(prompt[loc[0]:loc[1]]))
		if negatedIncludePrefix.MatchString(prompt[:loc[0]]) {
			continue
		}
		start := loc[1]
		for start < len(prompt) {
			quotes := quotedRanges(prompt[start:])
			if len(quotes) == 0 || quotes[0][0] != 0 {
				break
			}
			end := start + quotes[0][1]
			value := strings.Trim(prompt[start:end], `"“”'‘’`)
			entityType := "artist"
			if by := qualifiedBy.FindStringIndex(prompt[end:]); by != nil {
				artistStart := end + by[1]
				artistEnd := referenceTextEnd(prompt, artistStart)
				// A conjunction only separates recordings when followed by a
				// quoted reference; Simon and Garfunkel stays one credit.
				for _, next := range quotedRanges(prompt[artistStart:]) {
					before := prompt[artistStart : artistStart+next[0]]
					join := quotedJoinSuffix.FindStringIndex(before)
					if join != nil && artistStart+join[0] < artistEnd {
						artistEnd = artistStart + join[0]
						break
					}
				}
				artistStart, artistEnd = trimRange(prompt, artistStart, artistEnd)
				if artistEnd <= artistStart {
					break
				}
				value += " by " + prompt[artistStart:artistEnd]
				end, entityType = artistEnd, "track"
			}
			kind, scope, strength := referenceRole(intro, entityType)
			out = append(out, quotedReference{start, end, kind, value, scope, strength})
			join := quotedJoin.FindStringIndex(prompt[end:])
			if join == nil {
				break
			}
			start = end + join[1]
		}
	}
	return out
}

// quotedRanges accepts paired ASCII/Unicode quotes without treating the
// apostrophes in Brian Eno's or Don't Stop as opening/closing delimiters.
// Offsets remain byte offsets into the untouched user prompt.
func quotedRanges(s string) [][2]int {
	var out [][2]int
	start, closer := -1, rune(0)
	for pos, r := range s {
		if start < 0 {
			switch r {
			case '"':
				closer = '"'
			case '“':
				closer = '”'
			case '\'':
				if pos > 0 {
					previous, _ := utf8.DecodeLastRuneInString(s[:pos])
					if unicode.IsLetter(previous) || unicode.IsNumber(previous) {
						continue
					}
				}
				closer = '\''
			case '‘':
				closer = '’'
			default:
				continue
			}
			start = pos
			continue
		}
		if r == '\n' {
			start = -1
			continue
		}
		if r != closer {
			continue
		}
		end := pos + utf8.RuneLen(r)
		if r == '\'' || r == '’' {
			next, _ := utf8.DecodeRuneInString(s[end:])
			if unicode.IsLetter(next) || unicode.IsNumber(next) {
				continue
			}
		}
		if pos > start+utf8.RuneLen(r) {
			out = append(out, [2]int{start, end})
		}
		start = -1
	}
	return out
}

func overlapsQuotedReference(refs []quotedReference, start, end int) bool {
	for _, r := range refs {
		if start < r.end && end > r.start {
			return true
		}
	}
	return false
}
