// Package recognition performs deterministic, offline source recognition
// before either intent parser interprets the remaining language.
package recognition

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/musicconcepts"
)

const (
	Version         = "artist-first/v2"
	MaxPromptBytes  = 8 << 10
	MaxLexicalWords = 256
)

var (
	wordPattern     = regexp.MustCompile(`[\pL\pN][\pL\pN\pM'’&+._-]*`)
	referencePrefix = regexp.MustCompile(`(?i)(?:\b(?:like|similar to|inspired by|music by|songs by|tracks by|by|from|via|through|include|including|add|avoid|exclude|without|start with|starts with|begin with|end with|ends with|finish with|to|the artist|the track|the song)\s+|[-—–]\s*)$`)
	referenceClause = regexp.MustCompile(`(?i)\b(?:like|similar to|inspired by|music by|songs by|tracks by|include|including|avoid|exclude|start with|starts with|begin with|end with|ends with|finish with)\b`)
	listPrefix      = regexp.MustCompile(`(?i)(?:,|\band\b|\bor\b|&)\s*$`)
	negativePrefix  = regexp.MustCompile(`(?i)\b(?:avoid|exclude|excluding|without|no|not|nothing by|nothing from|skip)\s+$`)
	requiredPrefix  = regexp.MustCompile(`(?i)\b(?:include|including|add|must include|must have|start with|starts with|begin with|end with|ends with|finish with)\s+$`)
	startPrefix     = regexp.MustCompile(`(?i)\b(?:start with|starts with|begin with|from)\s+$`)
	endPrefix       = regexp.MustCompile(`(?i)\b(?:end with|ends with|finish with|to)\s+$`)
	trackSeparator  = regexp.MustCompile(`^\s*[-—–:]\s*`)
	bySuffix        = regexp.MustCompile(`(?i)\s+by\s+$`)
	possessiveTitle = regexp.MustCompile(`(?i)^['’]s\s+(?:(?:track|song|recording)\s+)?`)
	titleLead       = regexp.MustCompile(`(?i)^(?:include|including|add|play|queue|start with|begin with|end with|finish with)\s+`)
)

type token struct{ start, end int }
type span struct {
	start, end int
	text       string
	artists    []mbindex.ArtistIdentity
	truncated  bool
}

// IdentityLookup is the local-only, request-pinned lookup port used by the
// recognizer. The MusicBrainz store implements it; no network client does.
type IdentityLookup interface {
	SnapshotIdentity() mbindex.SnapshotIdentity
	LookupArtistNames(context.Context, []string) ([]mbindex.ArtistNameLookup, error)
	LookupArtistRecordings(context.Context, []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error)
}

// Apply adds immutable identity grounding and provider genres to an existing
// source extraction. Store must be an open immutable MusicBrainz snapshot.
func Apply(ctx context.Context, prompt string, source core.IntentTranslation, store IdentityLookup, vocabulary *genrevocab.Vocabulary) core.IntentTranslation {
	source.Recognition = core.RecognitionStatus{MatcherVersion: Version, GenreVocabulary: "embedded"}
	if vocabulary != nil {
		source.Recognition.GenreVocabulary = "installed"
		source.Recognition.GenreVocabularyHash = vocabulary.ContentSHA256
	}
	if genres, ok := store.(genreLookup); ok {
		if terms, err := genres.RecognitionGenres(ctx); err == nil && len(terms) > 0 {
			combined := genrevocab.Vocabulary{}
			if vocabulary != nil {
				combined = *vocabulary
				combined.Genres = append([]genrevocab.Genre(nil), vocabulary.Genres...)
			}
			for _, term := range terms {
				if term = strings.TrimSpace(term); term != "" && len(term) <= 128 {
					combined.Genres = append(combined.Genres, genrevocab.Genre{Name: term})
				}
			}
			vocabulary = &combined
			source.Recognition.GenreVocabulary += "+paipack"
		}
	}
	if store == nil {
		source.Recognition.ReferenceLookup = "unavailable"
		source.Recognition.Notices = append(source.Recognition.Notices, "Offline MusicBrainz identity lookup is unavailable; references were interpreted from the request text.")
		return addProviderGenres(prompt, source, vocabulary, nil)
	}
	identity := store.SnapshotIdentity()
	source.Recognition.ReferenceLookup = "available"
	source.Recognition.ReferenceSnapshot = identity.IndexVersion + ":" + identity.Snapshot
	if len(prompt) > MaxPromptBytes {
		return incomplete(source, "Reference lookup skipped because the request exceeds 8 KiB; the complete text remains available to the parser.")
	}
	tokens := lexicalTokens(prompt)
	if len(tokens) > MaxLexicalWords {
		return incomplete(source, "Reference lookup skipped because the request exceeds 256 lexical tokens; the complete text remains available to the parser.")
	}
	if len(tokens) == 0 {
		return addProviderGenres(prompt, source, vocabulary, nil)
	}

	lookupCtx, cancel := context.WithTimeout(ctx, mbindex.LookupTimeout)
	defer cancel()
	keys, occurrences := candidateKeys(prompt, tokens)
	matches := make(map[string]mbindex.ArtistNameLookup, len(keys))
	for at := 0; at < len(keys); at += mbindex.MaxLookupKeys {
		end := min(at+mbindex.MaxLookupKeys, len(keys))
		batch, err := store.LookupArtistNames(lookupCtx, keys[at:end])
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return incomplete(source, "Reference lookup was canceled; the complete text remains available to the parser.")
			}
			return incomplete(source, "Reference lookup did not complete; the complete text remains available to the parser.")
		}
		for _, result := range batch {
			if len(result.Candidates) > 0 {
				matches[result.NameKey] = result
			}
		}
	}
	var eligible []span
	for key, ranges := range occurrences {
		match, ok := matches[key]
		if !ok {
			continue
		}
		for _, bounds := range ranges {
			candidate := span{start: bounds[0], end: bounds[1], text: prompt[bounds[0]:bounds[1]], artists: match.Candidates, truncated: match.Truncated}
			if artistContext(prompt, candidate) {
				eligible = append(eligible, candidate)
			}
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		il, jl := len(core.NormalizeIdentityPart(eligible[i].text)), len(core.NormalizeIdentityPart(eligible[j].text))
		if il != jl {
			return il > jl
		}
		return eligible[i].start < eligible[j].start
	})
	accepted := make([]span, 0, len(eligible))
	for _, candidate := range eligible {
		if !spanOverlapsAny(candidate.start, candidate.end, accepted) {
			accepted = append(accepted, candidate)
		}
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].start < accepted[j].start })

	protected := make([][2]int, 0, len(accepted))
	for index, artist := range accepted {
		previousArtistEnd := 0
		if index > 0 {
			previousArtistEnd = accepted[index-1].end
		}
		nextArtistStart := len(prompt)
		if index+1 < len(accepted) {
			nextArtistStart = accepted[index+1].start
		}
		atom, bounds, err := recognizeReference(lookupCtx, prompt, source, store, artist, previousArtistEnd, nextArtistStart)
		if err != nil {
			source = incomplete(source, "Artist-scoped recording lookup did not complete; artist identity matches were retained.")
			atom, bounds = artistAtom(prompt, source, artist, store), [2]int{artist.start, artist.end}
		}
		source.Atoms = discardOverlappingInterpretations(source.Atoms, bounds[0], bounds[1])
		source.Atoms = append(source.Atoms, atom)
		protected = append(protected, bounds)
	}
	sort.SliceStable(source.Atoms, func(i, j int) bool {
		return source.Atoms[i].Evidence[0].Start < source.Atoms[j].Evidence[0].Start
	})
	return addProviderGenres(prompt, source, vocabulary, protected)
}

func incomplete(source core.IntentTranslation, notice string) core.IntentTranslation {
	source.Recognition.ReferenceLookup = "incomplete"
	source.Recognition.Incomplete = true
	source.Recognition.Notices = append(source.Recognition.Notices, notice)
	return source
}

func lexicalTokens(prompt string) []token {
	locs := wordPattern.FindAllStringIndex(prompt, -1)
	out := make([]token, len(locs))
	for i, loc := range locs {
		out[i] = token{loc[0], loc[1]}
	}
	return out
}

func candidateKeys(prompt string, tokens []token) ([]string, map[string][][2]int) {
	occurrences := make(map[string][][2]int)
	var keys []string
	for i := range tokens {
		for j := i; j < len(tokens); j++ {
			text := strings.TrimSpace(prompt[tokens[i].start:tokens[j].end])
			key := core.NormalizeIdentityPart(text)
			if key == "" {
				continue
			}
			if _, exists := occurrences[key]; !exists {
				keys = append(keys, text)
			}
			occurrences[key] = append(occurrences[key], [2]int{tokens[i].start, tokens[j].end})
		}
	}
	return keys, occurrences
}

func artistContext(prompt string, candidate span) bool {
	if strings.TrimSpace(prompt[:candidate.start]) == "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(prompt[candidate.end:])), "going to ") {
		return true
	}
	if quoted(prompt, candidate.start, candidate.end) || referencePrefix.MatchString(prompt[:candidate.start]) {
		return true
	}
	words := lexicalTokens(candidate.text)
	if len(words) == 1 {
		if negativePrefix.MatchString(prompt[:candidate.start]) && referenceTailBoundary(prompt[candidate.end:]) {
			return true
		}
		trimmed := strings.Trim(strings.TrimSpace(prompt), "\"'“”‘’.,;:!?")
		if strings.EqualFold(trimmed, candidate.text) {
			return true
		}
		clauseStart := strings.LastIndexAny(prompt[:candidate.start], ";.!?\n") + 1
		return referenceTailBoundary(prompt[candidate.end:]) && listPrefix.MatchString(prompt[clauseStart:candidate.start]) && referenceClause.MatchString(prompt[clauseStart:candidate.start])
	}
	// Bare multiword artists remain useful when their source spelling is
	// name-like. Lowercase descriptive phrases require explicit context.
	for _, part := range words {
		r, _ := utf8.DecodeRuneInString(candidate.text[part.start:part.end])
		if unicode.IsUpper(r) || unicode.Is(unicode.Lo, r) {
			continue
		}
		return false
	}
	return true
}

func referenceTailBoundary(suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" || strings.ContainsRune(",;.!?", rune(suffix[0])) {
		return true
	}
	if trackSeparator.MatchString(suffix) {
		return true
	}
	lower := strings.ToLower(suffix)
	return strings.HasPrefix(lower, "and ") || strings.HasPrefix(lower, "or ") || strings.HasPrefix(lower, "then ")
}

func quoted(prompt string, start, end int) bool {
	for _, pair := range [][2]string{{"\"", "\""}, {"“", "”"}, {"‘", "’"}} {
		left := strings.LastIndex(prompt[:start], pair[0])
		right := strings.Index(prompt[end:], pair[1])
		if left >= 0 && right >= 0 && !strings.Contains(prompt[left+len(pair[0]):start], pair[1]) {
			return true
		}
	}
	return false
}

func spanOverlapsAny(start, end int, spans []span) bool {
	for _, current := range spans {
		if start < current.end && end > current.start {
			return true
		}
	}
	return false
}

func recognizeReference(ctx context.Context, prompt string, source core.IntentTranslation, store IdentityLookup, artist span, previousArtistEnd, nextArtistStart int) (core.IntentAtom, [2]int, error) {
	if albums, ok := store.(albumLookup); ok {
		// Only explicit "album TITLE by ARTIST" syntax activates this port.
		prefix := prompt[:artist.start]
		if loc := regexp.MustCompile(`(?i)\balbum\s+(.+?)\s+by\s+$`).FindStringSubmatchIndex(prefix); loc != nil {
			title := cleanTitle(prefix[loc[2]:loc[3]])
			matches, truncated, err := albums.LookupArtistAlbums(ctx, artist.text, title)
			if err != nil {
				return core.IntentAtom{}, [2]int{}, err
			}
			if len(matches) > 0 {
				atom := referenceAtom(prompt, source, loc[2], artist.end, core.ReferenceAlbum, title+" by "+artist.text)
				atom.Grounding = &core.IdentityGrounding{Provider: "paipack", MatchedSpelling: prompt[loc[2]:artist.end], MatchType: "artist_scoped_album", SnapshotVersion: source.Recognition.ReferenceSnapshot, Candidates: matches, Truncated: truncated}
				return atom, [2]int{loc[2], artist.end}, nil
			}
		}
	}
	for _, adjacent := range adjacentTitles(prompt, source, artist, previousArtistEnd, nextArtistStart) {
		var queries []mbindex.ArtistRecordingQuery
		for _, identity := range artist.artists {
			queries = append(queries, mbindex.ArtistRecordingQuery{ArtistMBID: identity.MBID, Title: adjacent.title})
		}
		var results []mbindex.ArtistRecordingLookup
		for at := 0; at < len(queries); at += mbindex.MaxLookupKeys {
			last := min(at+mbindex.MaxLookupKeys, len(queries))
			batch, err := store.LookupArtistRecordings(ctx, queries[at:last])
			if err != nil {
				return core.IntentAtom{}, [2]int{}, err
			}
			results = append(results, batch...)
		}
		var candidates []core.IdentityCandidate
		truncated := artist.truncated
		for _, result := range results {
			truncated = truncated || result.Truncated
			for _, recording := range result.Candidates {
				if len(candidates) == mbindex.MaxLookupCandidates {
					truncated = true
					break
				}
				candidates = append(candidates, core.IdentityCandidate{Kind: core.ReferenceTrack, ID: recording.MBID, ArtistID: result.ArtistMBID, Name: recording.ArtistCredit, Title: recording.Title, Disambiguation: recording.Disambiguation})
			}
		}
		if len(candidates) > 0 {
			artistName := canonicalArtistName(artist.artists, candidates)
			atom := referenceAtom(prompt, source, adjacent.start, adjacent.end, core.ReferenceTrack, artistName+" — "+adjacent.title)
			atom.Grounding = &core.IdentityGrounding{Provider: provider(store), MatchedSpelling: prompt[adjacent.start:adjacent.end], MatchType: "artist_scoped_title", SnapshotVersion: store.SnapshotIdentity().IndexVersion + ":" + store.SnapshotIdentity().Snapshot, Candidates: candidates, Truncated: truncated}
			return atom, [2]int{adjacent.start, adjacent.end}, nil
		}
	}
	return artistAtom(prompt, source, artist, store), [2]int{artist.start, artist.end}, nil
}

func canonicalArtistName(artists []mbindex.ArtistIdentity, recordings []core.IdentityCandidate) string {
	artistID := ""
	for _, recording := range recordings {
		if artistID == "" {
			artistID = recording.ArtistID
		} else if artistID != recording.ArtistID {
			artistID = ""
			break
		}
	}
	if artistID != "" {
		for _, artist := range artists {
			if artist.MBID == artistID {
				return artist.Name
			}
		}
	}
	if len(artists) > 0 {
		return artists[0].Name
	}
	return ""
}

func provider(store IdentityLookup) string {
	if named, ok := store.(interface{ RecognitionProvider() string }); ok {
		return named.RecognitionProvider()
	}
	return "MusicBrainz"
}

func artistAtom(prompt string, source core.IntentTranslation, artist span, store IdentityLookup) core.IntentAtom {
	atom := referenceAtom(prompt, source, artist.start, artist.end, core.ReferenceArtist, artist.text)
	candidates := make([]core.IdentityCandidate, 0, len(artist.artists))
	matchType := "exact"
	for _, identity := range artist.artists {
		if len(candidates) == 0 {
			matchType = string(identity.MatchType)
		}
		candidates = append(candidates, core.IdentityCandidate{Kind: core.ReferenceArtist, ID: identity.MBID, Name: identity.Name, Disambiguation: identity.Disambiguation})
	}
	atom.Grounding = &core.IdentityGrounding{Provider: provider(store), MatchedSpelling: artist.text, MatchType: matchType, SnapshotVersion: source.Recognition.ReferenceSnapshot, Candidates: candidates, Truncated: artist.truncated}
	return atom
}

func referenceAtom(prompt string, source core.IntentTranslation, start, end int, kind core.ReferenceKind, value string) core.IntentAtom {
	atom := core.IntentAtom{ID: fmt.Sprintf("recognition:%d:%d", start, end), Kind: string(kind), Value: value, Scope: "playlist", Polarity: "positive", Strength: "preferred", Evidence: []core.SourceEvidence{{Text: prompt[start:end], Start: start, End: end, Explicit: true}}}
	for _, existing := range source.Atoms {
		if len(existing.Evidence) == 0 || existing.Evidence[0].Start > start || existing.Evidence[0].End < end {
			continue
		}
		switch existing.Kind {
		case "artist", "track", "album", "start", "destination", "exclude_artist", "entity_mention", "required_track", "require_artist":
			atom.Scope, atom.Polarity, atom.Strength = existing.Scope, existing.Polarity, existing.Strength
			if existing.Kind == "start" || existing.Kind == "destination" || existing.Kind == "exclude_artist" || existing.Kind == "required_track" || existing.Kind == "require_artist" {
				atom.Kind = existing.Kind
			}
			return atom
		}
	}
	prefix := prompt[max(0, start-48):start]
	switch {
	case negativePrefix.MatchString(prefix):
		atom.Polarity, atom.Strength = "negative", "required"
		if kind == core.ReferenceArtist {
			atom.Kind = "exclude_artist"
		}
	case startPrefix.MatchString(prefix):
		atom.Kind, atom.Scope, atom.Strength = "start", "journey_start", "required"
	case endPrefix.MatchString(prefix):
		atom.Kind, atom.Scope, atom.Strength = "destination", "journey_end", "required"
	case requiredPrefix.MatchString(prefix) && kind == core.ReferenceTrack:
		atom.Kind, atom.Strength = "required_track", "required"
	case requiredPrefix.MatchString(prefix) && kind == core.ReferenceArtist:
		atom.Kind, atom.Strength = "require_artist", "required"
	}
	return atom
}

type titleSpan struct {
	title      string
	start, end int
}

func adjacentTitles(prompt string, source core.IntentTranslation, artist span, previousArtistEnd, nextArtistStart int) []titleSpan {
	var out []titleSpan
	appendTitle := func(start, titleStart, end int) {
		if titleStart >= end {
			return
		}
		full := cleanTitle(prompt[titleStart:end])
		if full != "" {
			out = append(out, titleSpan{title: full, start: start, end: end})
		}
		// Descriptive suffixes remain available to genre/mood/sound parsing. Try
		// the full title first so real titles containing these words still win.
		if descriptor := regexp.MustCompile(`(?i)\s+(?:with|but|featuring|feat\.?|while)\s+`).FindStringIndex(prompt[titleStart:end]); descriptor != nil {
			shortEnd := titleStart + descriptor[0]
			short := cleanTitle(prompt[titleStart:shortEnd])
			if short != "" && !strings.EqualFold(short, full) {
				out = append(out, titleSpan{title: short, start: start, end: shortEnd})
			}
		}
	}
	if separator := trackSeparator.FindStringIndex(prompt[artist.end:]); separator != nil {
		start, end := artist.start, clauseEnd(prompt, artist.end+separator[1])
		if nextArtistStart > artist.end && nextArtistStart <= len(prompt) {
			between := prompt[artist.end:nextArtistStart]
			if connector := listPrefix.FindStringIndex(between); connector != nil {
				end = min(end, artist.end+connector[0])
			}
		}
		titleStart := artist.end + separator[1]
		appendTitle(start, titleStart, end)
		return out
	}
	if possessive := possessiveTitle.FindStringIndex(prompt[artist.end:]); possessive != nil {
		start, end := artist.start, clauseEnd(prompt, artist.end+possessive[1])
		appendTitle(start, artist.end+possessive[1], end)
		return out
	}
	if bySuffix.MatchString(prompt[:artist.start]) {
		by := bySuffix.FindStringIndex(prompt[:artist.start])
		end := artist.end
		start := clauseStart(prompt, by[0])
		if previousArtistEnd > start && previousArtistEnd < by[0] {
			start = previousArtistEnd
			if connector := regexp.MustCompile(`(?i)^\s*(?:,|\band\b|\bor\b|&)\s*`).FindStringIndex(prompt[start:by[0]]); connector != nil {
				start += connector[1]
			}
		}
		segment := prompt[start:by[0]]
		if lead := titleLead.FindStringIndex(segment); lead != nil {
			start += lead[1]
			segment = prompt[start:by[0]]
		}
		title := cleanTitle(segment)
		if title != "" {
			out = append(out, titleSpan{title: title, start: start, end: end})
		}
		return out
	}
	if referencePrefix.MatchString(prompt[:artist.start]) && artist.end < len(prompt) && unicode.IsSpace(rune(prompt[artist.end])) {
		end := clauseEnd(prompt, artist.end)
		if nextArtistStart > artist.end {
			end = min(end, nextArtistStart)
		}
		titleStart, titleEnd := trimSpaceRange(prompt, artist.end, end)
		if titleStart < titleEnd && len(lexicalTokens(prompt[titleStart:titleEnd])) <= 12 && nameLikeTitle(prompt[titleStart:titleEnd]) && !overlapsMusicalAtom(source.Atoms, titleStart, titleEnd) {
			out = append(out, titleSpan{title: cleanTitle(prompt[titleStart:titleEnd]), start: artist.start, end: titleEnd})
			return out
		}
	}
	// Quoting supplies explicit title context for otherwise adjacent wording.
	if match := regexp.MustCompile(`^\s*["“‘]([^"”’\n]+)["”’]`).FindStringSubmatchIndex(prompt[artist.end:]); match != nil {
		start, end := artist.start, artist.end+match[1]
		title := prompt[artist.end+match[2] : artist.end+match[3]]
		out = append(out, titleSpan{title: strings.TrimSpace(title), start: start, end: end})
	}
	return out
}

func trimSpaceRange(text string, start, end int) (int, int) {
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return start, end
}

func nameLikeTitle(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(value)
	return unicode.IsUpper(r) || unicode.Is(unicode.Lo, r)
}

func overlapsMusicalAtom(atoms []core.IntentAtom, start, end int) bool {
	for _, atom := range atoms {
		switch atom.Kind {
		case "genre", "style", "mood", "texture", "instrumentation", "vocal", "activity", "energy":
			for _, evidence := range atom.Evidence {
				if start < evidence.End && end > evidence.Start {
					return true
				}
			}
		}
	}
	return false
}

func cleanTitle(value string) string {
	value = strings.TrimSpace(value)
	for _, pair := range [][2]string{{"\"", "\""}, {"“", "”"}, {"‘", "’"}} {
		if strings.HasPrefix(value, pair[0]) && strings.HasSuffix(value, pair[1]) {
			value = strings.TrimSuffix(strings.TrimPrefix(value, pair[0]), pair[1])
			break
		}
	}
	return strings.TrimSpace(value)
}

func clauseStart(prompt string, before int) int {
	start := strings.LastIndexAny(prompt[:before], ",;.!?\n") + 1
	for start < before && unicode.IsSpace(rune(prompt[start])) {
		start++
	}
	return start
}

func clauseEnd(prompt string, after int) int {
	end := len(prompt)
	if at := strings.IndexAny(prompt[after:], ",;.!?\n"); at >= 0 {
		end = after + at
	}
	return end
}

func discardOverlappingInterpretations(atoms []core.IntentAtom, start, end int) []core.IntentAtom {
	out := make([]core.IntentAtom, 0, len(atoms))
	for _, atom := range atoms {
		overlaps := false
		for _, evidence := range atom.Evidence {
			// Replace interpretations owned by this complete occurrence. A wider
			// unresolved artist/title clause remains available to the established
			// title-only and album fallback paths.
			overlaps = overlaps || evidence.Start >= start && evidence.End <= end
		}
		if !overlaps || atom.Kind == "count" || atom.Kind == "duration" || strings.HasPrefix(atom.Kind, "temporal") {
			out = append(out, atom)
		}
	}
	return out
}

func addProviderGenres(prompt string, source core.IntentTranslation, vocabulary *genrevocab.Vocabulary, protected [][2]int) core.IntentTranslation {
	type term struct{ value, concept string }
	termsByName := map[string]term{}
	for _, concept := range musicconcepts.Concepts() {
		if concept.Kind != "genre" {
			continue
		}
		for _, value := range append([]string{concept.Value}, concept.Aliases...) {
			termsByName[strings.ToLower(value)] = term{concept.Value, concept.ID}
		}
	}
	if vocabulary != nil {
		for _, genre := range vocabulary.Genres {
			key := strings.ToLower(genre.Name)
			if _, reviewed := termsByName[key]; !reviewed {
				termsByName[key] = term{strings.ToLower(genre.Name), ""}
			}
		}
	}
	var terms []term
	for _, item := range termsByName {
		terms = append(terms, item)
	}
	sort.Slice(terms, func(i, j int) bool {
		if len(terms[i].value) != len(terms[j].value) {
			return len(terms[i].value) > len(terms[j].value)
		}
		if terms[i].value != terms[j].value {
			return terms[i].value < terms[j].value
		}
		return terms[i].concept < terms[j].concept
	})
	occupied := append([][2]int(nil), protected...)
	for _, atom := range source.Atoms {
		if atom.Kind == "genre" && len(atom.Evidence) > 0 {
			occupied = append(occupied, [2]int{atom.Evidence[0].Start, atom.Evidence[0].End})
		}
	}
	for _, item := range terms {
		pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(item.value))
		for _, loc := range pattern.FindAllStringIndex(prompt, -1) {
			if overlapsRanges(loc[0], loc[1], occupied) || !wordBoundary(prompt, loc[0], loc[1]) {
				continue
			}
			role := semanticRole(prompt, loc[0])
			source.Atoms = append(source.Atoms, core.IntentAtom{ID: fmt.Sprintf("genre:%d:%d", loc[0], loc[1]), ConceptID: item.concept, Kind: "genre", Value: item.value, Scope: role.scope, Polarity: role.polarity, Strength: role.strength, Degree: role.degree, Evidence: []core.SourceEvidence{{Text: prompt[loc[0]:loc[1]], Start: loc[0], End: loc[1], Explicit: true}}})
			occupied = append(occupied, [2]int{loc[0], loc[1]})
		}
	}
	sort.SliceStable(source.Atoms, func(i, j int) bool { return source.Atoms[i].Evidence[0].Start < source.Atoms[j].Evidence[0].Start })
	coordinateGenres(prompt, source.Atoms)
	return source
}

func coordinateGenres(prompt string, atoms []core.IntentAtom) {
	for i := 1; i < len(atoms); i++ {
		left, right := &atoms[i-1], &atoms[i]
		if left.Kind != "genre" || right.Kind != "genre" || left.Scope != right.Scope || len(left.Evidence) == 0 || len(right.Evidence) == 0 {
			continue
		}
		start, end := left.Evidence[0].End, right.Evidence[0].Start
		if start > end {
			continue
		}
		gap := strings.TrimSpace(strings.ToLower(prompt[start:end]))
		if gap == "or" && left.Polarity == "positive" && right.Polarity == "positive" {
			group := left.Group
			if group == "" {
				group = fmt.Sprintf("or:%d", left.Evidence[0].Start)
			}
			left.Group, right.Group = group, group
		}
		if gap == "and" || gap == "," || gap == "or" || gap == "nor" {
			if left.Polarity == "negative" && right.Polarity == "positive" {
				right.Polarity, right.Strength, right.Degree = left.Polarity, left.Strength, left.Degree
			}
			if left.Degree == "mostly" && right.Strength != "required" {
				right.Strength, right.Degree = "preferred", "mostly"
			}
		}
	}
}

type role struct{ scope, polarity, strength, degree string }

func semanticRole(prompt string, start int) role {
	r := role{"playlist", "positive", "essential", "plain"}
	prefix := strings.ToLower(prompt[max(0, start-64):start])
	if regexp.MustCompile(`(?:\bno|without|avoid|exclude|not)\s+$`).MatchString(prefix) {
		r.polarity, r.strength = "negative", "required"
	}
	if regexp.MustCompile(`(?:\bsome|a touch of|a bit of|influence)\s+$`).MatchString(prefix) {
		r.strength = "preferred"
	}
	if regexp.MustCompile(`(?:\bless|not too)\s+$`).MatchString(prefix) {
		r.polarity, r.strength, r.degree = "negative", "preferred", "reduced"
	}
	if regexp.MustCompile(`(?:\bstart|begin|opening)[^,;.!?]*$`).MatchString(prefix) {
		r.scope = "journey_start"
	}
	if regexp.MustCompile(`(?:\bend|finish|closing)[^,;.!?]*$`).MatchString(prefix) {
		r.scope = "journey_end"
	}
	return r
}

func overlapsRanges(start, end int, ranges [][2]int) bool {
	for _, bounds := range ranges {
		if start < bounds[1] && end > bounds[0] {
			return true
		}
	}
	return false
}

func wordBoundary(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
	}
	return true
}
