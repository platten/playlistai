package schema

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var decadePattern = regexp.MustCompile(`(?i)\b((?:18|19|20)[0-9]0)['’]?s\b`)
var negativePeriodPrefix = regexp.MustCompile(`(?i)\b(?:not|no|without|avoid|excluding|exclude|except|skip)\s+(?:(?:music|songs|tracks|recordings)\s+)?(?:from\s+)?(?:the\s+)?$`)
var emotionalWordPattern = regexp.MustCompile(`(?i)\b(romantic|melancholy|dreamy|joyful|relaxing|energetic)\b`)
var historicalRomanticPattern = regexp.MustCompile(`(?i)\b(classical|composer|composition|period|era|century|orchestral|romanticism)\b|\b18[0-9]{2}\b`)
var exclusionPrefixPattern = regexp.MustCompile(`(?i)\b(?:do not include|don't include|don’t include|do not play|don't play|no|not|without|avoid|skip|excluding|exclude|except|nothing by|nothing from)\b`)
var exclusionBoundaryPattern = regexp.MustCompile(`(?i)[;.!?\n]|,\s*(?:(?:music|songs|tracks)\s+(?:by|like)|(?:play|add)\b)|\b(?:but|instead|like|similar to|include|including|ending|transitioning)\b`)
var additiveOrQuantityPattern = regexp.MustCompile(`(?i)^\s+(?:(?:only|just|merely)\b|more than\b)`)
var emotionalNegativePrefix = regexp.MustCompile(`(?i)\b(?:not|no|without|avoid)(?:\s+(?:too|very))?\s*$`)
var vocalDescriptionPattern = regexp.MustCompile(`(?i)(?:\b(?:vocals?|voices?)$|^instrumental$)`)
var contrastExclusionPattern = regexp.MustCompile(`(?i)\s+but\s+(?:with\s+)?(?:no|not|without)\b`)
var requestBoilerplatePattern = regexp.MustCompile(`(?i)\b(?:please|give|make|find|me|a|an|some|any|random|playlist|of|with|songs|tracks|music|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|hundred|[0-9]+)\b`)

// A contrasting exclusion must not erase the affirmative half of a request.
// Ask the interpreter to repair omissions instead of guessing whether an
// unfamiliar phrase is a genre, mood, recording or artist.
func validateAffirmativeContrast(w Wire, prompt string) error {
	boundary := contrastExclusionPattern.FindStringIndex(prompt)
	if boundary == nil {
		return nil
	}
	prefix := strings.TrimSpace(prompt[:boundary[0]])
	if negative := exclusionPrefixPattern.FindStringIndex(prefix); negative != nil && negative[0] == 0 {
		return nil // a negative opening is not an affirmative clause to restore
	}
	if strings.Trim(strings.TrimSpace(requestBoilerplatePattern.ReplaceAllString(prefix, "")), ",- ") == "" {
		return nil // count and generic request controls need no musical clause
	}
	switch strings.ToLower(prefix) {
	case "", "music", "songs", "tracks", "anything", "a playlist", "make a playlist", "give me music":
		return nil
	}
	present := func(span string) bool { return strings.TrimSpace(span) != "" && containsReferenceWords(prefix, span) }
	covered := func(value, span string) bool {
		return present(span) || present(value) && containsReferenceWords(span, value)
	}
	for _, group := range [][]WirePreference{w.Genres, w.Styles, w.Moods, w.Instrumentation, w.Textures, {w.VocalPreference}} {
		for _, p := range group {
			if p.Value != "" && p.Influence != "negative" && covered(p.Value, p.Span) {
				return nil
			}
		}
	}
	for _, c := range w.EssentialCriteria {
		if covered(c.Value, c.Span) {
			return nil
		}
	}
	for _, group := range [][]WireReference{w.References, w.RequiredTracks, w.JourneyWaypoints, w.Destination} {
		for _, r := range group {
			if r.Influence != "negative" && covered(r.Value, r.Span) {
				return nil
			}
		}
	}
	for _, period := range w.Temporal {
		for _, e := range period.Evidence {
			if present(e.Text) {
				return nil
			}
		}
	}
	return fmt.Errorf("schema: affirmative request %q was omitted before the exclusion; preserve that phrase in its appropriate positive category or explicit reference", prefix)
}

func preserveEmotionalMeaning(w *Wire, prompt string) {
	occurrences := map[string]int{}
	for _, loc := range emotionalWordPattern.FindAllStringIndex(prompt, -1) {
		occurrences[strings.ToLower(prompt[loc[0]:loc[1]])]++
	}
	for _, loc := range emotionalWordPattern.FindAllStringIndex(prompt, -1) {
		value := prompt[loc[0]:loc[1]]
		// Repeated words can describe different journey stages or polarities.
		// A lexical repair cannot disambiguate those occurrences safely.
		if occurrences[strings.ToLower(value)] != 1 {
			continue
		}
		if strings.EqualFold(value, "romantic") && historicalRomanticPattern.MatchString(prompt) {
			continue
		}
		entity := false
		for _, group := range [][]WireReference{w.References, w.RequiredTracks, w.Destination, w.JourneyWaypoints} {
			for _, ref := range group {
				for _, pos := range literalPositions(prompt, ref.Span) {
					entity = entity || pos[0] <= loc[0] && pos[1] >= loc[1]
				}
			}
		}
		if entity {
			continue
		}
		influence := "positive"
		if emotionalNegativePrefix.MatchString(prompt[:loc[0]]) {
			influence = "negative"
		}
		// A correctly interpreted reduction ("less energetic", for example)
		// remains negative even when the narrow literal repair misses its cue.
		for _, group := range [][]WirePreference{w.Moods, w.Genres, w.Styles} {
			for _, p := range group {
				if strings.EqualFold(p.Value, value) && p.Influence == "negative" {
					influence = "negative"
				}
			}
		}
		scoped := false
		for _, c := range w.EssentialCriteria {
			scoped = scoped || strings.EqualFold(c.Value, value) && strings.HasPrefix(c.Scope, "journey_")
		}
		covered := false
		for i := range w.Moods {
			if strings.EqualFold(w.Moods[i].Value, value) {
				w.Moods[i].Influence = influence
				covered = true
			}
		}
		if !covered && !scoped {
			w.Moods = append(w.Moods, WirePreference{Value: value, Span: value, Explicit: true, Influence: influence})
		}
		for _, group := range []*[]WirePreference{&w.Genres, &w.Styles} {
			kept := (*group)[:0]
			for _, p := range *group {
				if strings.EqualFold(p.Value, value) {
					continue
				}
				kept = append(kept, p)
			}
			*group = kept
		}
		criteria := w.EssentialCriteria[:0]
		for _, c := range w.EssentialCriteria {
			if (c.Kind == "genre" || c.Kind == "style" || c.Kind == "mood") && strings.EqualFold(c.Value, value) {
				if influence == "negative" {
					continue
				}
				c.Kind = "mood"
			}
			criteria = append(criteria, c)
		}
		w.EssentialCriteria = criteria
	}
}

// An explicit vocal preference cannot simultaneously become a genre gate for
// the same source phrase. Keep its essential role when the model assigned one,
// but route its evidence to the vocal comparison rather than genre metadata.
func preserveVocalMeaning(w *Wire) {
	v := w.VocalPreference
	if v.Value == "" || !v.Explicit || !vocalDescriptionPattern.MatchString(strings.TrimSpace(v.Value)) {
		return
	}
	for _, group := range []*[]WirePreference{&w.Genres, &w.Styles} {
		kept := (*group)[:0]
		for _, p := range *group {
			if !strings.EqualFold(p.Value, v.Value) {
				kept = append(kept, p)
			}
		}
		*group = kept
	}
	kept := w.EssentialCriteria[:0]
	for _, c := range w.EssentialCriteria {
		if (c.Kind == "genre" || c.Kind == "style") && strings.EqualFold(c.Value, v.Value) {
			if v.Influence == "negative" {
				continue
			}
			c.Kind = "vocal"
		}
		kept = append(kept, c)
	}
	w.EssentialCriteria = kept
}

// Source polarity must be grounded in the negative clause, including list
// continuations. Merely finding a named artist elsewhere is not an exclusion.
func exclusionSpan(prompt, value string, names ...string) string {
	for _, loc := range exclusionPrefixPattern.FindAllStringIndex(prompt, -1) {
		tail := prompt[loc[1]:]
		if additiveOrQuantityPattern.MatchString(tail) {
			continue
		}
		positions := literalPositions(tail, value)
		for _, name := range names {
			positions = append(positions, literalPositions(tail, name)...)
		}
		for _, end := range exclusionBoundaryPattern.FindAllStringIndex(tail, -1) {
			insideName := false
			for _, pos := range positions {
				insideName = insideName || end[0] >= pos[0] && end[0] < pos[1]
			}
			if !insideName {
				tail = tail[:end[0]]
				break
			}
		}
		if containsReferenceWords(tail, value) {
			return prompt[loc[0] : loc[1]+len(tail)]
		}
	}
	return ""
}

func validateConstraintMeaning(w *Wire, prompt string) error {
	var excludedNames []string
	for _, c := range w.HardConstraints {
		if c.Kind == "exclude_artist" || c.Kind == "exclude_album" || c.Kind == "exclude_track" {
			excludedNames = append(excludedNames, c.Value)
		}
	}
	for i := range w.HardConstraints {
		c := &w.HardConstraints[i]
		if c.Kind != "exclude_artist" && c.Kind != "exclude_album" && c.Kind != "exclude_track" {
			continue
		}
		span := exclusionSpan(prompt, c.Value, excludedNames...)
		if span == "" {
			return fmt.Errorf("schema: exclusion of %q has no negative instruction in the request; remove the invented exclusion", c.Value)
		}
		c.Span = span
		for _, group := range [][]WireReference{w.RequiredTracks, w.Destination} {
			for _, ref := range group {
				conflict := c.Kind == "exclude_"+ref.Kind && core.NormalizeIdentityPart(c.Value) == core.NormalizeIdentityPart(ref.Value)
				if c.Kind == "exclude_artist" && (ref.Kind == "track" || ref.Kind == "album") {
					artist, _, ok := core.QualifiedReferenceParts(ref.Value)
					conflict = conflict || ok && core.NormalizeIdentityPart(artist) == core.NormalizeIdentityPart(c.Value)
				}
				if conflict {
					return fmt.Errorf("schema: required recording or destination %q conflicts with an exclusion", ref.Value)
				}
			}
		}
		for _, required := range w.HardConstraints {
			if required.Kind == strings.Replace(c.Kind, "exclude_", "require_", 1) && core.NormalizeIdentityPart(required.Value) == core.NormalizeIdentityPart(c.Value) {
				return fmt.Errorf("schema: %q is both required and excluded", c.Value)
			}
		}
	}
	return nil
}
