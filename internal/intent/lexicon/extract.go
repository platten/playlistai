// Package lexicon preserves explicit source facts before model interpretation.
// Its dictionary recognizes requested meaning, never evidence about a recording.
package lexicon

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

const Version = "source-atoms/v4"

var (
	durationPattern = regexp.MustCompile(`(?i)\b(` + tensNumber + `|` + smallNumber + `|an?|[0-9]{1,3})[\s\p{Pd}]*(minutes?|mins?|hours?|hrs?)\b`)
	periodPattern   = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?:st|nd|rd|th)?[\s\p{Pd}]+century\b|\b((?:18|19|20)[0-9]0)['’]?s\b`)
	referenceIntro  = regexp.MustCompile(`(?i)\b(?:similar to|inspired by|in the style of|along the lines of|the atmosphere of|the feel of|like|music by|songs by|tracks by|the artist|transitioning to|ending at|ending with|finish with|begin with|start with|from|through|via|to)\s+`)
	referenceEnd    = regexp.MustCompile(`(?i)[,:;.!?\n]|\s+(?:but|with|for|over|through|via|to|into|from|by the end|at the end|and then|that|themselves)\b`)
	negativeIntro   = regexp.MustCompile(`(?i)\b(?:do not include|don't include|don’t include|don't play|do not play|do not want|don't want|don’t want|nothing by|nothing from|excluding|exclude|without|except|avoid|skip|neither|nor|no|not)\s+`)
	negativeEnd     = regexp.MustCompile(`(?i)[;.!?\n]|\b(?:but|instead|rather than|like|similar to|include|including|ending|transitioning)\b`)
	entitySplit     = regexp.MustCompile(`(?i)\s*(?:,|\band\b|\bor\b|\bnor\b|&)\s*`)
	startMarker     = regexp.MustCompile(`(?i)\b(?:starts?|starting|begins?|beginning)\s+(?:with\s+)?`)
	endMarker       = regexp.MustCompile(`(?i)\b(?:ends?|ending|finishes?|finishing)\s+(?:with\s+|at\s+)?`)
	softPrefix      = regexp.MustCompile(`(?i)\b(?:mostly|mainly|preferably|ideally|some|a bit of|a touch of|touch of)\s*$`)
	negativePrefix  = regexp.MustCompile(`(?i)\b(?:not|no|without|avoid|skip|rather than|nothing)(?:\s+(?:too|very|much))?\s*$`)
	reducedPrefix   = regexp.MustCompile(`(?i)\b(?:less|not too|nothing too)\s*$`)
	strictPrefix    = regexp.MustCompile(`(?i)\b(?:must be|must have|only|strictly|always|absolutely)\s*$`)
	quotePattern    = regexp.MustCompile(`"[^"\n]+"|“[^”\n]+”`)
)

type mention struct {
	start, end           int
	kind, value, concept string
}

// Extract is deterministic and offline. Unknown or ambiguous wording remains
// available to the model; it is not silently replaced with a known parent genre.
func Extract(prompt string) core.IntentTranslation {
	x := core.IntentTranslation{Version: Version + "+" + musicconcepts.Version}
	add := func(kind, value, scope, polarity, strength, degree, concept string, start, end int) {
		if start < 0 || end > len(prompt) || start >= end || value == "" {
			return
		}
		x.Atoms = append(x.Atoms, core.IntentAtom{ID: fmt.Sprintf("%s:%d:%d", kind, start, end), Kind: kind, Value: value, Scope: scope, Polarity: polarity, Strength: strength, Degree: degree, ConceptID: concept,
			Evidence: []core.SourceEvidence{{Text: prompt[start:end], Start: start, End: end, Explicit: true}}})
	}
	for _, p := range durationMentions(prompt) {
		add("duration", strconv.Itoa(p.seconds), "playlist", "positive", "required", "plain", "", p.start, p.end)
	}
	for _, p := range durationRangePattern.FindAllStringSubmatchIndex(prompt, -1) {
		if !insideQuoted(prompt, p[0], p[1]) {
			add("duration_range", prompt[p[0]:p[1]], "playlist", "positive", "required", "plain", "", p[0], p[1])
		}
	}
	for _, p := range regexp.MustCompile(`(?i)\b(?:released|recorded|composed|written)\s+in\s+[12][0-9]{3}(?:\s+or\s+[12][0-9]{3})+\b`).FindAllStringIndex(prompt, -1) {
		if !insideQuoted(prompt, p[0], p[1]) {
			add("temporal_alternatives", prompt[p[0]:p[1]], "playlist", "positive", "required", "plain", "", p[0], p[1])
		}
	}
	for _, p := range regexp.MustCompile(`(?i)\b(?:more|less)\s+energy\b`).FindAllStringIndex(prompt, -1) {
		if !insideQuoted(prompt, p[0], p[1]) && negativeContextStart(prompt[:p[0]]) < 0 {
			add("relative_energy", prompt[p[0]:p[1]], "playlist", "positive", "preferred", "plain", "", p[0], p[1])
		}
	}
	if n, ok := TrackCount(prompt); ok {
		for _, p := range countSpans(prompt) {
			add("count", strconv.Itoa(n), "playlist", "positive", "required", "plain", "", p[0], p[1])
		}
	}
	for _, p := range periodPattern.FindAllStringSubmatchIndex(prompt, -1) {
		if insideQuoted(prompt, p[0], p[1]) {
			continue
		}
		basis, first, last := "original_release", 0, 0
		if p[2] >= 0 {
			n, _ := strconv.Atoi(prompt[p[2]:p[3]])
			first, last = (n-1)*100+1, n*100
			if strings.Contains(strings.ToLower(prompt), "classical") || strings.Contains(strings.ToLower(prompt), "compos") {
				basis = "composition"
			}
		} else {
			first, _ = strconv.Atoi(prompt[p[4]:p[5]])
			last = first + 9
		}
		if first > 0 {
			kind, polarity, start := "temporal", "positive", p[0]
			if negative := negativeCalendarPrefix.FindStringIndex(prompt[:p[0]]); negative != nil {
				kind, polarity, start = "temporal_exclusion", "negative", negative[0]
			}
			add(kind, fmt.Sprintf("%s:%d:%d", basis, first, last), scopeAt(prompt, p[0]), polarity, "required", "plain", "", start, p[1])
		}
	}
	for _, p := range yearRangePattern.FindAllStringSubmatchIndex(prompt, -1) {
		if insideQuoted(prompt, p[0], p[1]) {
			continue
		}
		first, _ := strconv.Atoi(prompt[p[4]:p[5]])
		last, _ := strconv.Atoi(prompt[p[6]:p[7]])
		if first > last {
			continue
		}
		basis := "original_release"
		if p[2] >= 0 && (strings.EqualFold(prompt[p[2]:p[3]], "composed") || strings.EqualFold(prompt[p[2]:p[3]], "written")) {
			basis = "composition"
		}
		kind, polarity, start := "temporal", "positive", p[0]
		if negative := negativeCalendarPrefix.FindStringIndex(prompt[:p[0]]); negative != nil {
			kind, polarity, start = "temporal_exclusion", "negative", negative[0]
		}
		add(kind, fmt.Sprintf("%s:%d:%d", basis, first, last), scopeAt(prompt, p[0]), polarity, "required", "plain", "", start, p[1])
	}
	known := conceptMentions(prompt)
	for _, r := range requiredTrackOccurrences(prompt) {
		e := r.Evidence[0]
		add("required_track", r.Query, "playlist", "positive", "required", "plain", "", e.Start, e.End)
	}
	// First protect explicitly introduced entities. Descriptive clauses such as
	// "from quiet to loud" must not turn into artist endpoints.
	for _, loc := range referenceIntro.FindAllStringIndex(prompt, -1) {
		end := referenceTextEnd(prompt, loc[1])
		start, end := trimRange(prompt, loc[1], end)
		intro := strings.ToLower(strings.TrimSpace(prompt[loc[0]:loc[1]]))
		if (intro == "begin with" || intro == "start with" || intro == "finish with" || strings.HasPrefix(intro, "ending")) && negatedIncludePrefix.MatchString(prompt[:loc[0]]) {
			continue
		}
		if end <= start {
			continue
		}
		if !insideQuoted(prompt, start, end) && quantityReference(prompt[start:end]) {
			continue // "I'd like 10 tracks" is a quantity, not an artist
		}
		if stop := artistReferenceSuffix.FindStringIndex(prompt[start:end]); stop != nil {
			end = start + stop[0]
		}
		entityType := "artist"
		if p := regexp.MustCompile(`(?i)^(early|late)\s+`).FindStringSubmatchIndex(prompt[start:end]); p != nil {
			add("reference_era", strings.ToLower(prompt[start+p[2]:start+p[3]]), "playlist", "positive", "preferred", "plain", "", start+p[2], start+p[3])
		}
		explicitEntity := strings.Contains(intro, "by") || intro == "the artist"
		if strings.HasPrefix(strings.ToLower(prompt[start:end]), "the album ") || strings.HasPrefix(strings.ToLower(prompt[start:end]), "album ") {
			entityType = "album"
			explicitEntity = true
		}
		if strings.HasPrefix(strings.ToLower(prompt[start:end]), "the track ") || strings.HasPrefix(strings.ToLower(prompt[start:end]), "track ") || strings.HasPrefix(strings.ToLower(prompt[start:end]), "the song ") || strings.HasPrefix(strings.ToLower(prompt[start:end]), "song ") {
			entityType = "track"
			explicitEntity = true
		}
		if strings.HasPrefix(strings.ToLower(prompt[start:end]), "the artist ") || strings.HasPrefix(prompt[start:end], "artist ") {
			explicitEntity = true
		}
		// Lower-case type prefixes are syntax. A capitalized literal name such
		// as "Artist A" must retain every word of its catalog query.
		if p := regexp.MustCompile(`^(?:(?:the )?(?:artist|album|track|song)\s+|early\s+|late\s+)`).FindStringIndex(prompt[start:end]); p != nil {
			start += p[1]
		}
		start, end = trimRange(prompt, start, end)
		value := prompt[start:end]
		if p := regexp.MustCompile(`(?i)^(.+?)['’]s\s+(album|track)\s+(.+)$`).FindStringSubmatch(value); p != nil {
			entityType = strings.ToLower(p[2])
			explicitEntity = true
			value = p[3] + " by " + p[1]
		}
		if strings.Contains(value, " - ") || strings.Contains(value, " — ") {
			entityType = "track"
			explicitEntity = true
		}
		if genericReference(value) || descriptiveRange(prompt, start, end, known) && !explicitEntity && ((intro != "like" && intro != "similar to") || !nameLike(value)) {
			continue
		}
		if intro == "to" && !hasSourceJourney(prompt, loc[0]) && !strings.HasPrefix(strings.ToLower(prompt[:loc[0]]), "transition") {
			continue
		}
		if (intro == "from" || intro == "to" || intro == "via" || intro == "through") && !explicitEntity && !nameLike(value) && !compoundNameLike(value) {
			continue
		}
		kind, scope, strength := referenceRole(intro, entityType)
		if entityType == "artist" && ambiguousEntityMention(value) {
			add("entity_mention", value, scope, "positive", strength, "plain", "", start, end)
			continue
		}
		segments := splitRanges(prompt, start, end)
		if entityType != "artist" {
			segments = [][2]int{{start, end}}
		}
		for _, segment := range segments {
			query := prompt[segment[0]:segment[1]]
			if entityType != "artist" {
				query = value
			}
			add(kind, query, scope, "positive", strength, "plain", "", segment[0], segment[1])
		}
	}
	if artist, start, end := onlyArtistMention(prompt); artist != "" {
		add("require_artist", artist, "playlist", "positive", "required", "plain", "", start, end)
	}
	// A short bare name is protected only with name-like syntax. Unknown bare
	// categories still reach the open-vocabulary model unchanged.
	if lenEntityAtoms(x.Atoms) == 0 {
		start, end := trimRange(prompt, 0, len(prompt))
		if p := regexp.MustCompile(`(?i)^(?:play|put on|queue)\s+`).FindStringIndex(prompt[start:end]); p != nil {
			start += p[1]
		}
		if comma := strings.Index(prompt, ","); comma > 0 {
			end = comma
		}
		value := strings.TrimSpace(prompt[start:end])
		exactConcept := false
		negativeOpening := false
		if loc := negativeIntro.FindStringIndex(prompt[start:end]); loc != nil && loc[0] == 0 {
			negativeOpening = true
		}
		for _, m := range known {
			exactConcept = exactConcept || m.start == start && m.end == end
		}
		if len(strings.Fields(value)) <= 4 && nameLike(value) && !negativeOpening && !exactConcept && !fullyDescriptive(prompt, start, end, known) && !strings.ContainsAny(value, ".!?:") && !genericReference(value) {
			add("artist", value, "playlist", "positive", "preferred", "plain", "", start, end)
		}
	}
	for _, loc := range negativeIntro.FindAllStringIndex(prompt, -1) {
		if strings.HasPrefix(strings.ToLower(prompt[loc[1]:]), "only ") || strings.HasPrefix(strings.ToLower(prompt[loc[1]:]), "more than ") {
			continue
		}
		end := len(prompt)
		if stop := negativeEnd.FindStringIndex(prompt[loc[1]:]); stop != nil {
			end = loc[1] + stop[0]
		}
		parts := negativeEntityRanges(prompt, loc[1], end)
		for _, part := range parts {
			start, last := part[0], part[1]
			value := strings.TrimSpace(strings.TrimSuffix(prompt[start:last], " themselves"))
			if strings.HasPrefix(strings.ToLower(value), "no more ") {
				start += len("no more ")
				value = value[len("no more "):]
			}
			last = start + len(value)
			covered := false
			for _, m := range known {
				covered = covered || start < m.end && last > m.start
			}
			if value == "" || covered || descriptiveRange(prompt, start, last, known) || genericReference(value) || strings.Contains(strings.ToLower(value), "back to back") {
				continue
			}
			corroborated := nameLike(value) || compoundNameLike(value) || len(parts) > 1 && len(strings.Fields(value)) <= 2
			for _, a := range x.Atoms {
				corroborated = corroborated || entityKind(a.Kind) && strings.EqualFold(a.Value, value)
			}
			if !corroborated {
				continue
			}
			kind := "exclude_artist"
			if ambiguousEntityMention(value) {
				kind = "entity_mention"
			}
			add(kind, value, "playlist", "negative", "required", "plain", "", loc[0], end)
		}
	}
	x.Atoms = append(x.Atoms, attachedArtistExclusions(prompt, x.Atoms)...)
	for _, m := range known {
		if m.kind == "mood" && m.value == "romantic" && regexp.MustCompile(`(?i)\b(classical|composer|composition|period|era|century|romanticism)\b`).MatchString(prompt) {
			continue
		}
		if overlapsEntity(x.Atoms, m.start, m.end) || insideQuoted(prompt, m.start, m.end) {
			continue
		}
		polarity, strength, degree, spanStart := "positive", "preferred", "plain", m.start
		prefix := prompt[:m.start]
		if m.kind == "genre" {
			strength = "essential"
		}
		if loc := negativeContextStart(prefix); loc >= 0 {
			polarity = "negative"
			spanStart = loc
			if m.kind == "genre" || m.kind == "vocal" {
				strength = "required"
			}
		}
		if loc := composedNegativeStart(prompt, m.start, known); loc >= 0 {
			polarity, spanStart = "negative", loc
			if m.kind == "genre" || m.kind == "vocal" {
				strength = "required"
			}
		}
		if loc := reducedPrefix.FindStringIndex(prefix); loc != nil {
			polarity = "negative"
			spanStart = loc[0]
			strength = "preferred"
			degree = "reduced"
		}
		softened := false
		if loc := softPrefix.FindStringIndex(prefix); loc != nil {
			softened = true
			strength = "preferred"
			spanStart = loc[0]
			if strings.EqualFold(strings.TrimSpace(prefix[loc[0]:]), "mostly") || strings.EqualFold(strings.TrimSpace(prefix[loc[0]:]), "mainly") {
				degree = "mostly"
			}
		}
		if loc := composedPreferredStart(prompt, m.start, known); loc >= 0 {
			softened, strength, spanStart = true, "preferred", loc
			if strings.HasPrefix(strings.ToLower(prefix[loc:]), "mostly ") || strings.HasPrefix(strings.ToLower(prefix[loc:]), "mainly ") {
				degree = "mostly"
			}
		}
		if additivePrefix.MatchString(prefix) {
			softened = true
			strength = "preferred"
		}
		if strictPrefix.MatchString(prefix) && !additivePrefix.MatchString(prefix) {
			strength = "required"
		}
		if strings.HasPrefix(strings.ToLower(prompt[m.end:]), " influence") {
			strength = "preferred"
		}
		// "no singing/no vocals" expresses instrumental presence; other vocal
		// qualities retain their own negative polarity rather than banning vocals.
		value := m.value
		if m.kind == "vocal" && polarity == "negative" && (value == "vocal" || value == "vocals" || value == "singing") {
			value = "instrumental"
			polarity = "positive"
			if concept, ok := musicconcepts.Find("vocal", value); ok {
				m.concept = concept.ID
			}
		}
		if m.kind == "vocal" && value == "instrumental" && !softened {
			strength = "required"
		}
		add(m.kind, value, scopeAt(prompt, m.start), polarity, strength, degree, m.concept, spanStart, m.end)
	}
	spacing := regexp.MustCompile(`(?i)\b(?:no (?:repeat artists|back-to-back artists|back to back artists)|no repeat artists back to back|no repeated artists back to back|no adjacent artist repeats)\b`)
	for _, p := range spacing.FindAllStringIndex(prompt, -1) {
		add("spacing", "true", "playlist", "positive", "required", "plain", "", p[0], p[1])
	}
	energyPhrases := []struct{ pattern, scope, value string }{{`(?i)\b(?:starts? easy|starts? calm|begins? calmly)\b`, "journey_start", "0.2"}, {`(?i)\b(?:picks? up the pace|builds? energy)\b`, "journey_via", "0.8"}, {`(?i)\b(?:cools? down|winds? down)\b`, "journey_end", "0.2"}}
	for _, p := range energyPhrases {
		for _, loc := range regexp.MustCompile(p.pattern).FindAllStringIndex(prompt, -1) {
			add("energy", p.value, p.scope, "positive", "preferred", "plain", "", loc[0], loc[1])
		}
	}
	filtered := x.Atoms[:0]
	for _, a := range x.Atoms {
		if (a.Kind == "temporal" || a.Kind == "temporal_exclusion" || a.Kind == "temporal_alternatives" || a.Kind == "relative_energy" || a.Kind == "duration" || a.Kind == "duration_range" || a.Kind == "count") && overlapsEntity(x.Atoms, a.Evidence[0].Start, a.Evidence[0].End) {
			continue
		}
		filtered = append(filtered, a)
	}
	x.Atoms = filtered
	// Coordination propagates polarity/softness without turning every named
	// category into a conjunction. Keep occurrence IDs even for repeated words.
	sort.SliceStable(x.Atoms, func(i, j int) bool { return x.Atoms[i].Evidence[0].Start < x.Atoms[j].Evidence[0].Start })
	for i := 1; i < len(x.Atoms); i++ {
		a, b := &x.Atoms[i-1], &x.Atoms[i]
		if !musicalKind(a.Kind) || !musicalKind(b.Kind) || a.Scope != b.Scope {
			continue
		}
		left, right := a.Evidence[0].End, b.Evidence[0].Start
		if left > right {
			continue
		}
		gap := strings.TrimSpace(strings.ToLower(prompt[left:right]))
		if gap == "or" && a.Kind == "genre" && b.Kind == "genre" && a.Polarity == "positive" && b.Polarity == "positive" {
			group := a.Group
			if group == "" {
				group = fmt.Sprintf("or:%d", a.Evidence[0].Start)
			}
			a.Group = group
			b.Group = group
		}
		if gap == "and" || gap == "," || gap == "or" || gap == "nor" {
			if a.Polarity == "negative" && b.Polarity == "positive" && (gap != "," || a.Kind == b.Kind) && !negativeIntro.MatchString(b.Evidence[0].Text) {
				b.Polarity = a.Polarity
				b.Strength = a.Strength
				b.Degree = a.Degree
				b.Evidence[0].Start = a.Evidence[0].Start
				b.Evidence[0].Text = prompt[b.Evidence[0].Start:b.Evidence[0].End]
			}
			if a.Degree == "mostly" && b.Strength != "required" {
				b.Strength = "preferred"
				b.Degree = "mostly"
			}
		}
	}
	return x
}

func conceptMentions(prompt string) []mention {
	var candidates []mention
	for _, c := range musicconcepts.Concepts() {
		for _, alias := range append([]string{c.Value}, c.Aliases...) {
			if alias == "" {
				continue
			}
			pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(alias))
			for _, p := range pattern.FindAllStringIndex(prompt, -1) {
				if !boundary(prompt, p[0], true) || !boundary(prompt, p[1], false) {
					continue
				}
				candidates = append(candidates, mention{p[0], p[1], c.Kind, c.Value, c.ID})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		li, lj := candidates[i].end-candidates[i].start, candidates[j].end-candidates[j].start
		if li != lj {
			return li > lj
		}
		return candidates[i].start < candidates[j].start
	})
	var out []mention
	for _, c := range candidates {
		overlap := false
		for _, m := range out {
			// A guitar riff is both an arrangement/texture description and
			// explicit instrument evidence. Parent genres remain suppressed.
			nestedInstrument := c.kind == "instrumentation" && m.kind == "texture" && c.start >= m.start && c.end <= m.end
			overlap = overlap || c.start < m.end && c.end > m.start && !nestedInstrument
		}
		if !overlap {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func boundary(s string, p int, left bool) bool {
	if p <= 0 || p >= len(s) {
		return true
	}
	var r rune
	if left {
		r, _ = utf8.DecodeLastRuneInString(s[:p])
	} else {
		r, _ = utf8.DecodeRuneInString(s[p:])
	}
	// Do not extract a parent from an unrecognized hyphenated compound.
	return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-'
}
func trimRange(s string, start, end int) (int, int) {
	for start < end {
		r, n := utf8.DecodeRuneInString(s[start:end])
		if !unicode.IsSpace(r) && !strings.ContainsRune(",;: ", r) {
			break
		}
		start += n
	}
	for end > start {
		r, n := utf8.DecodeLastRuneInString(s[start:end])
		if !unicode.IsSpace(r) && !strings.ContainsRune(",;: ", r) {
			break
		}
		end -= n
	}
	return start, end
}
func splitRanges(s string, start, end int) [][2]int {
	var out [][2]int
	at := start
	for _, p := range entitySplit.FindAllStringIndex(s[start:end], -1) {
		a, b := trimRange(s, at, start+p[0])
		if a < b {
			out = append(out, [2]int{a, b})
		}
		at = start + p[1]
	}
	a, b := trimRange(s, at, end)
	if a < b {
		out = append(out, [2]int{a, b})
	}
	return out
}
func insideQuoted(s string, start, end int) bool {
	for _, p := range quotePattern.FindAllStringIndex(s, -1) {
		if start >= p[0] && end <= p[1] {
			return true
		}
	}
	return false
}
func entityKind(k string) bool {
	return k == "artist" || k == "track" || k == "album" || k == "start" || k == "destination" || k == "exclude_artist" || k == "require_artist" || k == "required_track" || k == "entity_mention"
}
func musicalKind(k string) bool {
	switch k {
	case "genre", "style", "mood", "instrumentation", "vocal", "texture", "activity":
		return true
	}
	return false
}
func lenEntityAtoms(atoms []core.IntentAtom) int {
	n := 0
	for _, a := range atoms {
		if entityKind(a.Kind) {
			n++
		}
	}
	return n
}
func overlapsEntity(atoms []core.IntentAtom, start, end int) bool {
	for _, a := range atoms {
		if !entityKind(a.Kind) {
			continue
		}
		for _, e := range a.Evidence {
			left, right := e.Start, e.End
			// Exclusion evidence includes its negator/list clause. Only the
			// literal identity is shielded; later "and no screaming" remains
			// an independent musical instruction.
			if a.Kind == "exclude_artist" || a.Kind == "entity_mention" && a.Polarity == "negative" {
				if offset := strings.Index(e.Text, a.Value); offset >= 0 {
					left, right = e.Start+offset, e.Start+offset+len(a.Value)
				}
			}
			if start < right && end > left {
				return true
			}
		}
	}
	return false
}
func genericReference(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "this", "that", "this one", "that one", "music", "songs", "tracks", "the playlist", "quiet", "loud", "easy", "up", "down", "back":
		return true
	}
	return false
}
func nameLike(v string) bool {
	words := strings.Fields(v)
	if len(words) == 0 || len(words) > 5 {
		return false
	}
	for i, w := range words {
		if i > 0 && i < len(words)-1 {
			switch w {
			case "of", "the", "a", "an", "in", "on", "at", "de", "la", "van", "von":
				continue
			}
		}
		r, _ := utf8.DecodeRuneInString(w)
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
func descriptiveRange(s string, start, end int, known []mention) bool {
	for _, m := range known {
		if m.start >= start && m.end <= end {
			return true
		}
	}
	return durationPattern.MatchString(s[start:end]) || periodPattern.MatchString(s[start:end]) || yearRangePattern.MatchString(s[start:end])
}
func hasSourceJourney(s string, before int) bool {
	start := strings.LastIndexAny(s[:before], ",;.!?") + 1
	return regexp.MustCompile(`(?i)\b(?:from|transitioning|leading|moving)\b`).MatchString(s[start:before])
}
func scopeAt(s string, pos int) string {
	// Explicit stage markers are local to their sentence. Later global requests
	// ("I like electronic music") therefore do not acquire the final-stage scope.
	begin := strings.LastIndexAny(s[:pos], ".!?;") + 1
	prefix := strings.ToLower(s[begin:pos])
	if regexp.MustCompile(`(?i)\b(?:opening|first|initial)\s+(?:section|part|stage)\s*$`).MatchString(prefix) {
		return "journey_start"
	}
	if regexp.MustCompile(`(?i)\b(?:closing|last|final)\s+(?:section|part|stage)\s*$`).MatchString(prefix) {
		return "journey_end"
	}
	lastStart, lastEnd := -1, -1
	for _, p := range startMarker.FindAllStringIndex(prefix, -1) {
		lastStart = p[0]
	}
	for _, p := range endMarker.FindAllStringIndex(prefix, -1) {
		lastEnd = p[0]
	}
	if lastEnd > lastStart {
		return "journey_end"
	}
	if lastStart >= 0 {
		return "journey_start"
	}
	if regexp.MustCompile(`(?i)\b(?:transitioning|leading|moving)\s+to\b`).MatchString(s[pos:]) {
		return "journey_start"
	}
	journey := regexp.MustCompile(`(?i)\bfrom\s+(.+?)\s+(?:to|into)\s+(.+?)(?:[,.;!?]|$)`).FindStringSubmatchIndex(s)
	if journey != nil {
		for _, via := range regexp.MustCompile(`(?i)\b(?:via|through)\s+`).FindAllStringIndex(s[journey[0]:journey[1]], -1) {
			v := journey[0] + via[1]
			next := journey[1]
			if v < journey[4] {
				next = journey[4]
			}
			if pos >= v && pos < next {
				return "journey_via"
			}
		}
		if pos >= journey[2] && pos < journey[3] {
			return "journey_start"
		}
		if pos >= journey[4] && pos < journey[5] {
			return "journey_end"
		}
	}
	return "playlist"
}

// IsMusicalKind is shared by schema reconciliation without a provider dependency.
func IsMusicalKind(kind string) bool { return musicalKind(kind) }
