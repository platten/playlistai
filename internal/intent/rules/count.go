package rules

import (
	"regexp"
	"strconv"
	"strings"
)

// Match longer phrases first so "twenty five" and "half a dozen" cannot
// become five or twelve. This is language syntax, independent of music names.
const smallNumber = `(?:nineteen|eighteen|seventeen|sixteen|fifteen|fourteen|thirteen|twelve|eleven|ten|nine|eight|seven|six|five|four|three|two|one|zero)`
const tensNumber = `(?:ninety|eighty|seventy|sixty|fifty|forty|thirty|twenty)(?:[ -]+` + smallNumber + `)?`

var countPattern = regexp.MustCompile(`(?i)\b(half a dozen|a dozen|a handful|a few|(?:one |a )?hundred|` + tensNumber + `|` + smallNumber + `|\d{1,9})\s*(?:songs?|tracks?|tunes?|of them|long)\b`)
var numberValues = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11,
	"twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
	"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
	"seventy": 70, "eighty": 80, "ninety": 90, "hundred": 100,
	"a dozen": 12, "half a dozen": 6, "a handful": 8, "a few": 5,
	"one hundred": 100, "a hundred": 100,
}

// TrackCount extracts an explicit playlist length for both rules and local
// model output. Musical reference names and dates without a count noun do not
// establish a length. Quoted track/album titles are left intact.
func TrackCount(prompt string) (int, bool) {
	spans := countSpans(prompt)
	if len(spans) == 0 {
		return 0, false
	}
	span := spans[len(spans)-1]
	phrase := strings.ToLower(prompt[span[2]:span[3]])
	if n, err := strconv.Atoi(phrase); err == nil {
		return n, true
	}
	if n, ok := numberValues[phrase]; ok {
		return n, true
	}
	var n int
	for _, word := range strings.Fields(strings.ReplaceAll(phrase, "-", " ")) {
		n += numberValues[word]
	}
	return n, true
}

func countSpans(prompt string) [][]int {
	var spans [][]int
	for _, span := range countPattern.FindAllStringSubmatchIndex(prompt, -1) {
		// Preserve quoted names such as an album called "Ten Songs".
		prefix := prompt[:span[0]]
		if strings.Count(prefix, `"`)%2 != 0 || strings.LastIndex(prefix, "“") > strings.LastIndex(prefix, "”") {
			continue
		}
		spans = append(spans, span)
	}
	return spans
}

// Keep offsets in the original description usable as source evidence.
func maskTrackCounts(prompt string) string {
	masked := []byte(prompt)
	for _, span := range countSpans(prompt) {
		for i := span[0]; i < span[1]; i++ {
			masked[i] = ' '
		}
	}
	return string(masked)
}
