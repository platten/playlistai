package schema

import (
	"regexp"
	"strings"
)

var namedEndPrefix = regexp.MustCompile(`(?i)\b(?:ends?|ending|finish(?:es|ing)?)\s+(?:with|at|on)\s+(?:(?:the\s+)?artist\s+)?["“']?\s*$`)
var journeyToPrefix = regexp.MustCompile(`(?i)\b(?:to|into)\s+(?:(?:the\s+)?artist\s+)?["“']?\s*$`)

// Recover an omitted named artist endpoint from a source-grounded reference,
// never from an inferred anchor. "Similar to Artist" is not an endpoint;
// "from A to B" needs an already established journey contract.
func preserveNamedDestination(w *Wire, prompt string) {
	if len(w.Destination) > 0 {
		return
	}
	last := -1
	var destination WireReference
	for _, group := range [][]WireReference{w.JourneyWaypoints, w.References} {
		for _, ref := range group {
			if ref.Kind != "artist" || !ref.Explicit || ref.Influence == "negative" {
				continue
			}
			at := lastLiteralFold(prompt, ref.Value)
			if at < 0 && referenceGrounded(ref.Span, ref) {
				at = lastLiteralFold(prompt, ref.Span)
			}
			if at <= last {
				continue
			}
			prefix := prompt[:at]
			if namedEndPrefix.MatchString(prefix) || w.Mode == "journey" && len(w.JourneyWaypoints) >= 2 && journeyToPrefix.MatchString(prefix) {
				destination, last = ref, at
			}
		}
	}
	if last >= 0 {
		w.Destination = []WireReference{destination}
		w.Mode = "journey"
	}
}

func lastLiteralFold(text, value string) int {
	matches := literalPositions(text, value)
	if len(matches) == 0 {
		return -1
	}
	return matches[len(matches)-1][0]
}

func literalPositions(text, value string) [][]int {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(value))
	if err != nil {
		return nil
	}
	return pattern.FindAllStringIndex(text, -1)
}
