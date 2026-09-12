package lexicon

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/platten/playlistai/internal/core"
)

var (
	indirectNegativePrefix = regexp.MustCompile(`(?i)\b(?:do not|don't|don’t|never)\s+(?:want|need|like|include|play)(?:\s+(?:any|very|too))?\s*$`)
	neitherPrefix          = regexp.MustCompile(`(?i)\bneither\s*$`)
	additivePrefix         = regexp.MustCompile(`(?i)\bnot\s+(?:only|just|merely)\s*$`)
	negativeCalendarPrefix = regexp.MustCompile(`(?i)\b(?:do not want|don't want|don’t want|do not include|don't include|don’t include|not|no|without|avoid|excluding|exclude|except|skip)\s+(?:(?:any|music|songs|tracks|pieces|recordings)\s+)*(?:from\s+)?(?:the\s+)?$`)
	yearRangePattern       = regexp.MustCompile(`(?i)\b(?:(released|recorded|composed|written)\s+)?(?:between|from)\s+([12][0-9]{3})\s+(?:and|to|through)\s+([12][0-9]{3})\b`)
	durationRangePattern   = regexp.MustCompile(`(?i)\b([0-9]{1,3})\s*(?:-|–|—|to)\s*([0-9]{1,3})\s*(minutes?|mins?|hours?|hrs?)\b`)
	wideReferenceEnd       = regexp.MustCompile(`(?i)[:;.!?\n]|\s+(?:but|with|for|over|through|via|to|into|from|by the end|at the end|and then|that|themselves)\b`)
	calendarVerbPattern    = regexp.MustCompile(`(?i)\b(released|recorded|composed|written)\b`)
	calendarYearPattern    = regexp.MustCompile(`\b[0-9]{4}\b`)
	calendarBoundsPattern  = regexp.MustCompile(`(?i)\b(?:before|after|until|since|older|newer)\b`)
	calendarSinglePattern  = regexp.MustCompile(`(?i)\b(released|recorded|composed|written)\s+(?:in|during)\s+([12][0-9]{3})\b`)
	calendarChoiceSuffix   = regexp.MustCompile(`(?i)^\s+(?:or|and)\s+[12][0-9]{3}\b`)
	negativeListBoundary   = regexp.MustCompile(`(?i)\s+\b(?:nor|or)\b\s+`)
	negatedQuantityPrefix  = regexp.MustCompile(`(?i)\b(?:not|no|without|avoid|never)(?:\s+(?:a|an|another|exactly))?\s*$`)
	artistReferenceSuffix  = regexp.MustCompile(`(?i)\s+(?:only\b|released\b|recorded\b|and\s+(?:finish|end|begin|start)\b)`)
	onlyArtistPrefix       = regexp.MustCompile(`(?i)\b(?:songs|tracks|music|playlist)\s+(?:only|exclusively)\s+by\s+([^,.;!?]+)`)
	onlyArtistSuffix       = regexp.MustCompile(`(?i)\b(?:songs|tracks|music)\s+by\s+([^,.;!?]+?)\s+only(?:\s*[,.;!?]|$)`)
	onlyArtistDated        = regexp.MustCompile(`(?i)\b(?:songs|tracks|music)\s+by\s+([^,.;!?]+),\s*(?:released|recorded)\s+(?:between|from)\s+[12][0-9]{3}\s+(?:and|to|through)\s+[12][0-9]{3}\s+only(?:\s*[,.;!?]|$)`)
	preferredClause        = regexp.MustCompile(`(?i)\b(?:prefer|preferably|ideally|mostly|mainly|some|a bit of|a touch of)\s+(?:a\s+|an\s+)?`)
	contrastOperator       = regexp.MustCompile(`(?i)\b(?:over|rather than|instead of)\s+(?:an?\s+)?$`)
	negativeClause         = regexp.MustCompile(`(?i)\b(?:not|no|without|avoid)\s+(?:(?:their|his|her|the|later|early|late)\s+)*`)
)

type durationMention struct {
	start, end, seconds int
}

func fullyDescriptive(prompt string, start, end int, known []mention) bool {
	text := []byte(prompt[start:end])
	for _, m := range known {
		if m.start >= start && m.end <= end {
			for i := m.start - start; i < m.end-start; i++ {
				text[i] = ' '
			}
		}
	}
	return strings.IndexFunc(string(text), unicode.IsLetter) < 0
}

// Adjacent descending units form one quantity; separate clauses and repeated
// units do not. Source offsets cover the entire compound expression.
func durationMentions(prompt string) []durationMention {
	var out []durationMention
	previousFactor := 0
	for _, p := range durationPattern.FindAllStringSubmatchIndex(prompt, -1) {
		if insideQuoted(prompt, p[0], p[1]) || durationRangeOverlap(prompt, p[0], p[1]) {
			continue
		}
		phrase := strings.ToLower(prompt[p[2]:p[3]])
		n, err := strconv.Atoi(phrase)
		if err != nil {
			if phrase == "a" || phrase == "an" {
				n = 1
			} else {
				for _, word := range strings.Fields(strings.ReplaceAll(phrase, "-", " ")) {
					n += numberValues[word]
				}
			}
		}
		factor := durationFactor(prompt[p[4]:p[5]])
		if n <= 0 || n*factor > 86400 {
			continue
		}
		if len(out) > 0 {
			last := &out[len(out)-1]
			gap := strings.TrimSpace(strings.ToLower(prompt[last.end:p[0]]))
			if (gap == "" || gap == "and") && factor < previousFactor && last.seconds+n*factor <= 86400 {
				last.end = p[1]
				last.seconds += n * factor
				previousFactor = factor
				continue
			}
		}
		out = append(out, durationMention{p[0], p[1], n * factor})
		previousFactor = factor
	}
	positive := out[:0]
	for _, p := range out {
		if !negatedQuantityPrefix.MatchString(prompt[:p.start]) {
			positive = append(positive, p)
		}
	}
	return positive
}

func durationFactor(unit string) int {
	if strings.HasPrefix(strings.ToLower(unit), "h") {
		return 3600
	}
	return 60
}

func durationRangeOverlap(prompt string, start, end int) bool {
	for _, p := range durationRangePattern.FindAllStringIndex(prompt, -1) {
		if start < p[1] && end > p[0] {
			return true
		}
	}
	return false
}

func quantityReference(value string) bool {
	quantityEnd := func(start, end int) bool {
		tail := strings.TrimSpace(strings.ToLower(value[end:]))
		return start == 0 && (tail == "" || strings.HasPrefix(tail, "of "))
	}
	for _, p := range countSpans(value) {
		if quantityEnd(p[0], p[1]) {
			return true
		}
	}
	for _, p := range durationMentions(value) {
		if quantityEnd(p.start, p.end) {
			return true
		}
	}
	return false
}

func negativeContextStart(prefix string) int {
	for _, pattern := range []*regexp.Regexp{negativePrefix, indirectNegativePrefix, neitherPrefix} {
		if loc := pattern.FindStringIndex(prefix); loc != nil {
			return loc[0]
		}
	}
	return -1
}

// Commas followed by an ampersand, and "and the", can be part of a single
// artist name. Preserve a whole candidate instead of asserting a split. A
// grounded model interpretation may still identify a list of separate artists.
func ambiguousEntityMention(value string) bool {
	return strings.Contains(value, ",") && strings.Contains(value, "&") || strings.Contains(strings.ToLower(value), " and the ")
}

func compoundNameLike(value string) bool {
	if !ambiguousEntityMention(value) {
		return false
	}
	for _, part := range splitRanges(value, 0, len(value)) {
		name := value[part[0]:part[1]]
		if strings.HasPrefix(strings.ToLower(name), "the ") {
			name = name[len("the "):]
		}
		if !nameLike(name) {
			return false
		}
	}
	return true
}

func negativeEntityRanges(prompt string, start, end int) [][2]int {
	var clauses [][2]int
	at := start
	for _, p := range negativeListBoundary.FindAllStringIndex(prompt[start:end], -1) {
		clauses = append(clauses, [2]int{at, start + p[0]})
		at = start + p[1]
	}
	clauses = append(clauses, [2]int{at, end})
	var out [][2]int
	for _, p := range clauses {
		left, right := trimRange(prompt, p[0], p[1])
		if compoundNameLike(prompt[left:right]) {
			out = append(out, [2]int{left, right})
		} else {
			out = append(out, splitRanges(prompt, left, right)...)
		}
	}
	return out
}

func referenceRole(intro, entityType string) (kind, scope, strength string) {
	kind, scope, strength = entityType, "playlist", "preferred"
	switch {
	case intro == "from" || intro == "begin with" || intro == "start with":
		kind, scope, strength = "start", "journey_start", "required"
	case intro == "via" || intro == "through":
		scope, strength = "journey_via", "required"
	case intro == "to" || strings.HasPrefix(intro, "ending") || strings.HasPrefix(intro, "finish") || intro == "transitioning to":
		kind, scope, strength = "destination", "journey_end", "required"
	}
	return
}

// OnlyArtist recognizes an affirmative output-domain restriction. A similarity
// request, endpoint or the additive "not only" construction is not sufficient.
func OnlyArtist(prompt string) string {
	artist, _, _ := onlyArtistMention(prompt)
	return artist
}

func onlyArtistMention(prompt string) (string, int, int) {
	for _, pattern := range []*regexp.Regexp{onlyArtistPrefix, onlyArtistSuffix, onlyArtistDated} {
		for _, p := range pattern.FindAllStringSubmatchIndex(prompt, -1) {
			if insideQuoted(prompt, p[0], p[1]) || negatedQuantityPrefix.MatchString(prompt[:p[0]]) {
				continue
			}
			start, end := trimRange(prompt, p[2], p[3])
			if stop := artistReferenceSuffix.FindStringIndex(prompt[start:end]); stop != nil {
				end = start + stop[0]
			}
			if start < end && !strings.Contains(strings.ToLower(prompt[start:end]), " not ") {
				return prompt[start:end], start, end
			}
		}
	}
	return "", 0, 0
}

func composedPreferredStart(prompt string, pos int, known []mention) int {
	begin := strings.LastIndexAny(prompt[:pos], ",;:.!?\n") + 1
	for _, p := range preferredClause.FindAllStringIndex(prompt[begin:pos], -1) {
		start := begin + p[0]
		if additivePrefix.MatchString(prompt[:start]) || negativeContextStart(prompt[:start]) >= 0 {
			continue
		}
		if descriptorGap(prompt, begin+p[1], pos, known) {
			return start
		}
	}
	return -1
}

func composedNegativeStart(prompt string, pos int, known []mention) int {
	begin := strings.LastIndexAny(prompt[:pos], ",;:.!?\n") + 1
	if p := contrastOperator.FindStringIndex(prompt[begin:pos]); p != nil {
		// "over" can describe layered sounds. Only treat it as contrast when
		// attached to an explicit preference, not "warm synth over loud drums".
		if strings.HasPrefix(strings.ToLower(prompt[begin+p[0]:]), "over ") && !preferredClause.MatchString(prompt[begin:begin+p[0]]) {
			return -1
		}
		for _, m := range known {
			if m.start >= begin && m.end <= begin+p[0] {
				return begin + p[0]
			}
		}
	}
	for _, p := range negativeClause.FindAllStringIndex(prompt[begin:pos], -1) {
		start := begin + p[0]
		if descriptorGap(prompt, begin+p[1], pos, known) && !additivePrefix.MatchString(prompt[start:pos]) {
			return start
		}
	}
	return -1
}

func descriptorGap(prompt string, start, end int, known []mention) bool {
	gap := []byte(prompt[start:end])
	for _, m := range known {
		if m.start >= start && m.end <= end {
			for i := m.start - start; i < m.end-start; i++ {
				gap[i] = ' '
			}
		}
	}
	for _, word := range strings.Fields(strings.ToLower(string(gap))) {
		switch word {
		case "a", "an", "and", "or", "sound", "sounds", "feel", "music", "power", "cheerful":
		default:
			return false
		}
	}
	return true
}

func referenceTextEnd(prompt string, start int) int {
	end := len(prompt)
	if stop := wideReferenceEnd.FindStringIndex(prompt[start:]); stop != nil {
		end = start + stop[0]
	}
	if ambiguousEntityMention(prompt[start:end]) {
		return end
	}
	if stop := referenceEnd.FindStringIndex(prompt[start:]); stop != nil {
		return start + stop[0]
	}
	return end
}

func candidateReference(a core.IntentAtom) core.IntentReference {
	return core.IntentReference{Kind: core.ReferenceArtist, Query: a.Value, Influence: core.Influence(a.Polarity), Evidence: a.Evidence}
}

// ReconcileFallback keeps an ambiguous full mention available to catalog
// resolution. The legacy rules splitter is not independent identity evidence.
func ReconcileFallback(intent core.MusicIntent, extracted core.IntentTranslation) core.MusicIntent {
	for _, a := range extracted.Atoms {
		if a.Kind != "entity_mention" {
			continue
		}
		kept := intent.References[:0]
		for _, r := range intent.References {
			if r.Influence != core.Influence(a.Polarity) || !wordsContain(a.Value, r.Query) {
				kept = append(kept, r)
			}
		}
		intent.References = kept
		intent.References = append(intent.References, candidateReference(a))
		if a.Polarity == "negative" {
			removeCandidateExclusions(&intent, a)
		}
	}
	return Reconcile(intent, extracted)
}

func hasCandidateInterpretation(intent core.MusicIntent, a core.IntentAtom) bool {
	remaining := a.Value
	for _, group := range [][]core.IntentReference{intent.References, intent.Journey.Waypoints} {
		for _, r := range group {
			if r.Influence != core.Influence(a.Polarity) || r.Kind != core.ReferenceArtist || !wordsContain(a.Value, r.Query) {
				continue
			}
			if wordsContain(r.Query, a.Value) {
				return true
			}
			remaining = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(r.Query)).ReplaceAllString(remaining, " ")
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(remaining), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if word != "and" && word != "or" {
			return false
		}
	}
	return true
}

func removeCandidateExclusions(intent *core.MusicIntent, a core.IntentAtom) {
	kept := intent.HardConstraints[:0]
	for _, c := range intent.HardConstraints {
		if c.Kind != "exclude_artist" || !wordsContain(a.Value, c.Value) {
			kept = append(kept, c)
		}
	}
	intent.HardConstraints = kept
}

func applyCandidateRole(intent *core.MusicIntent, a core.IntentAtom) {
	if a.Strength != "required" {
		return
	}
	if a.Polarity == "negative" {
		removeCandidateExclusions(intent, a)
		for _, r := range intent.References {
			if r.Kind == core.ReferenceArtist && r.Influence == core.InfluenceNegative && wordsContain(a.Value, r.Query) {
				intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: r.Query, Supported: true, Evidence: a.Evidence})
			}
		}
		return
	}
	if a.Scope != "journey_start" && a.Scope != "journey_end" && a.Scope != "journey_via" {
		return
	}
	r := candidateReference(a)
	intent.Mode = core.ModeJourney
	listed := false
	for _, ref := range intent.References {
		listed = listed || ref.Kind == r.Kind && ref.Influence == r.Influence && strings.EqualFold(ref.Query, r.Query)
	}
	if !listed {
		intent.References = append(intent.References, r)
	}
	kept := intent.Journey.Waypoints[:0]
	for _, waypoint := range intent.Journey.Waypoints {
		if !wordsContain(a.Value, waypoint.Query) {
			kept = append(kept, waypoint)
		}
	}
	intent.Journey.Waypoints = kept
	if a.Scope == "journey_start" {
		intent.Start = &r
		intent.Journey.Waypoints = append([]core.IntentReference{r}, intent.Journey.Waypoints...)
	} else {
		intent.Journey.Waypoints = append(intent.Journey.Waypoints, r)
		if a.Scope == "journey_end" {
			intent.Destination = &r
		}
	}
}

func negativeCandidateOwnsReference(prompt string, a core.IntentAtom, r core.IntentReference) bool {
	if a.Polarity != "negative" || r.Influence == core.InfluenceNegative {
		return false
	}
	for _, source := range a.Evidence {
		if !wordsContain(source.Text, r.Query) {
			continue
		}
		for _, e := range r.Evidence {
			if e.Start >= source.Start && e.End <= source.End && e.Start >= 0 && e.End > e.Start {
				return true
			}
		}
		if strings.Count(strings.ToLower(prompt), strings.ToLower(r.Query)) == 1 {
			return true
		}
	}
	return false
}

// A model may understand calendar wording outside the deterministic range
// grammar. Retain it only when both bounds, the date basis and its local scope
// are supported by a literal, positive source occurrence outside entity names.
func groundedModelPeriod(period core.TemporalRequirement, prompt string, atoms []core.IntentAtom) (core.TemporalRequirement, bool) {
	if period.StartYear < 1 || period.EndYear < period.StartYear || period.EndYear > 9999 {
		return period, false
	}
	for _, e := range period.Evidence {
		if !e.Explicit || e.Text == "" || negativeIntro.MatchString(e.Text) || calendarBoundsPattern.MatchString(e.Text) {
			continue
		}
		firstPresent, lastPresent := false, false
		for _, year := range calendarYearPattern.FindAllString(e.Text, -1) {
			firstPresent = firstPresent || year == strconv.Itoa(period.StartYear)
			lastPresent = lastPresent || year == strconv.Itoa(period.EndYear)
		}
		if !firstPresent || !lastPresent {
			continue
		}
		start := e.Start
		if start < 0 || e.End != start+len(e.Text) || e.End > len(prompt) || prompt[start:e.End] != e.Text {
			if strings.Count(prompt, e.Text) != 1 {
				continue
			}
			start = strings.Index(prompt, e.Text)
		}
		end := start + len(e.Text)
		boundSyntax := false
		if period.StartYear == period.EndYear {
			for _, loc := range calendarSinglePattern.FindAllStringSubmatchIndex(e.Text, -1) {
				if e.Text[loc[4]:loc[5]] == strconv.Itoa(period.StartYear) && !calendarChoiceSuffix.MatchString(prompt[start+loc[1]:]) {
					boundSyntax = true
				}
			}
		} else {
			for _, loc := range yearRangePattern.FindAllStringSubmatchIndex(e.Text, -1) {
				boundSyntax = boundSyntax || e.Text[loc[4]:loc[5]] == strconv.Itoa(period.StartYear) && e.Text[loc[6]:loc[7]] == strconv.Itoa(period.EndYear)
			}
		}
		if !boundSyntax {
			continue
		}
		verb := calendarVerbPattern.FindStringSubmatchIndex(e.Text)
		if verb == nil || insideQuoted(prompt, start, end) || overlapsEntity(atoms, start, end) {
			continue
		}
		verbStart := start + verb[0]
		if negativeContextStart(prompt[:verbStart]) >= 0 || negativeCalendarPrefix.MatchString(prompt[:verbStart]) {
			continue
		}
		basis := "original_release"
		if strings.EqualFold(e.Text[verb[2]:verb[3]], "composed") || strings.EqualFold(e.Text[verb[2]:verb[3]], "written") {
			basis = "composition"
		}
		if period.Basis != basis {
			continue
		}
		period.Scope = scopeAt(prompt, verbStart)
		period.Evidence = []core.SourceEvidence{{Text: e.Text, Start: start, End: end, Explicit: true}}
		return period, true
	}
	return period, false
}
