// Package rules is a dependency-free, always-available IntentParser: it turns a
// natural-language prompt into a core.MusicIntent with regexes and keyword
// tables. It is the fallback when no local LLM is loaded, and the deterministic
// baseline the llama backend is measured against.
package rules

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
)

// Parser implements ports.IntentParser.
type Parser struct{}

// New returns a rules parser.
func New() *Parser { return &Parser{} }

// Info implements ports.IntentParser.
func (*Parser) Info() ports.ParserInfo {
	return ports.ParserInfo{Name: "rules", Backend: "rules", Version: "rules/v12", Ready: true, ContractVersion: core.CurrentIntentVersion, Evidence: true}
}

// Parse implements ports.IntentParser. It never returns an error; an unparsable
// prompt yields an intent with no seeds, which the caller surfaces to the user.
func (*Parser) Parse(_ context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	prompt := strings.TrimSpace(in.Prompt)
	lower := strings.ToLower(prompt)

	intent := core.MusicIntent{
		OriginalDescription: prompt,
		VerificationPolicy:  core.VerifiedOnly,
		Version:             core.CurrentIntentVersion,
		Controls: core.IntentControls{
			TotalTrackCount:      core.DefaultCount,
			AudioWeight:          core.DefaultCreativity,
			CooccurrenceWeight:   1 - core.DefaultCreativity,
			Discovery:            0.10,
			ArtistDiversity:      0.7,
			TransitionSmoothness: float64(core.DefaultLookback-1) / 9,
		},
	}

	musicalText := maskTrackCounts(prompt)
	seeds, mode := extractSeeds(musicalText, strings.ToLower(musicalText), in.NowPlaying, in.RecentTracks)
	onlyArtist := OnlyArtist(prompt)
	if onlyArtist != "" {
		seeds, mode = []string{onlyArtist}, core.ModeSimilar
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "require_artist", Value: onlyArtist, Evidence: sourceEvidence(prompt, onlyArtist, true)})
	}
	for _, seed := range seeds {
		ref := typedReference(prompt, seed, catalogReferenceKind(seed), core.InfluencePositive)
		intent.References = append(intent.References, ref)
		if mode == core.ModeJourney {
			intent.Journey.Waypoints = append(intent.Journey.Waypoints, ref)
		}
	}
	intent.Mode = mode
	intent.RequiredTracks = extractRequiredTracks(prompt)
	for _, artist := range extractArtistExcludes(prompt) {
		evidence := sourceEvidence(prompt, artist, true)
		intent.References = append(intent.References, core.IntentReference{
			Kind: core.ReferenceArtist, Query: artist, Influence: core.InfluenceNegative, Evidence: evidence,
		})
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: "exclude_artist", Value: artist, Supported: true, Evidence: evidence,
		})
	}
	// Entity names are identity evidence, not musical evidence. Mask only the
	// resolved textual references while keeping byte offsets for source spans.
	semanticText := maskReferenceText(musicalText, append(append([]core.IntentReference(nil), intent.References...), intent.RequiredTracks...))
	intent.Preferences = extractSemanticPreferences(semanticText)
	intent.EssentialCriteria = extractEssentialCriteria(semanticText, intent.Preferences)

	if n, ok := TrackCount(prompt); ok {
		intent.Controls.TotalTrackCount = n
	}
	intent.Controls.AudioWeight = extractCreativity(lower)
	intent.Controls.CooccurrenceWeight = 1 - intent.Controls.AudioWeight
	intent.Controls.Discovery = extractNoise(lower)
	lookback := extractLookback(lower)
	intent.Controls.TransitionSmoothness = float64(lookback-1) / 9

	if explicitArtistSpacing.MatchString(lower) {
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: "no_back_to_back_artist", Value: "true", Supported: true, Evidence: sourceEvidence(prompt, explicitArtistSpacing.FindString(lower), true),
		})
	} else {
		intent.Controls.ArtistDiversity = 0.3
	}
	intent.Unsupported, intent.HardConstraints = extractUnsupportedRequirements(semanticText, intent.Unsupported, intent.HardConstraints)
	intent.Journey.EnergyTrajectory = extractEnergyTrajectory(lower)
	if _, trajectory := CategoryJourney(prompt, nil); len(trajectory) > 0 {
		intent.Journey.EnergyTrajectory = trajectory
	}

	intent = lexicon.Reconcile(intent, lexicon.Extract(prompt))
	out := intent.Normalized()
	out.Mode = mode // Normalized() would flip an unset mode to journey for >=2 seeds
	if intent.Start != nil || intent.Destination != nil || len(intent.Journey.EnergyTrajectory) > 0 {
		out.Mode = core.ModeJourney
	}
	out.NotesForUser = summarize(out)
	return out, nil
}

func catalogReferenceKind(seed string) core.ReferenceKind {
	for _, separator := range []string{" - ", " – ", " — "} {
		if strings.Contains(seed, separator) {
			return core.ReferenceTrack
		}
	}
	return core.ReferenceArtist
}

func extractEnergyTrajectory(lower string) []core.EnergyPoint {
	switch {
	case strings.Contains(lower, "build energy"), strings.Contains(lower, "rising energy"), strings.Contains(lower, "gets more energetic"):
		return []core.EnergyPoint{{Position: 0, Energy: 0.2}, {Position: 1, Energy: 0.8}}
	case strings.Contains(lower, "wind down"), strings.Contains(lower, "falling energy"), strings.Contains(lower, "gets calmer"):
		return []core.EnergyPoint{{Position: 0, Energy: 0.8}, {Position: 1, Energy: 0.2}}
	default:
		return nil
	}
}

func typedReference(prompt, value string, kind core.ReferenceKind, influence core.Influence) core.IntentReference {
	evidence := sourceEvidence(prompt, value, true)
	// When a name repeats a category ("electronic music like Electronic"),
	// mask the reference clause, not the earlier musical instruction.
	for _, pattern := range []*regexp.Regexp{reLike, reByArtist} {
		if match := pattern.FindStringSubmatchIndex(prompt); match != nil {
			if at := strings.Index(strings.ToLower(prompt[match[2]:match[3]]), strings.ToLower(value)); at >= 0 {
				start := match[2] + at
				evidence = []core.SourceEvidence{{Text: prompt[start : start+len(value)], Start: start, End: start + len(value), Explicit: true}}
				break
			}
		}
	}
	return core.IntentReference{
		Kind: kind, Query: value, Influence: influence, Evidence: evidence,
	}
}

func sourceEvidence(prompt, span string, explicit bool) []core.SourceEvidence {
	start := strings.Index(strings.ToLower(prompt), strings.ToLower(span))
	if start < 0 {
		return []core.SourceEvidence{{Text: span, Start: -1, End: -1, Explicit: explicit}}
	}
	return []core.SourceEvidence{{Text: prompt[start : start+len(span)], Start: start, End: start + len(span), Explicit: explicit}}
}

// --- seeds ---------------------------------------------------------------

var (
	reJourney   = regexp.MustCompile(`(?i)\b(?:from|between)\s+(.+?)\s+(?:to|into|and then|→|->)\s+(.+?)(?:\s+via\s+(.+?))?(?:\s*[,.;]|\s+but\b|\s+with\b|\s+for\b|$)`)
	reLike      = regexp.MustCompile(`(?i)\b(?:kinda like|kind of like|sort of like|stuff like|songs like|tracks like|things like|music like|similar to|sounds? like|in the style of|reminiscent of|along the lines of|inspired by|like)\s+(.+?)(?:\s*[,.;]|\s+but\b|\s+from\b|\s+with\b|\s+that\b|\s+for\b|\s+\d|$)`)
	reByArtist  = regexp.MustCompile(`(?i)\b(?:music|songs?|tracks?)\s+by\s+(.+?)(?:\s*[,.;]|\s+but\b|\s+with\b|\s+for\b|$)`)
	reThis      = regexp.MustCompile(`(?i)\b(this one|this track|this song|this|current(?:ly playing)?|what'?s playing|now playing|keep (?:it )?going|keep playing|more of this|in this vein|same vibe|same energy)\b`)
	reSeedSplit = regexp.MustCompile(`(?i)\s*(?:,|\band\b|&|\+|\bwith\b)\s*`)
	reLeadVerb  = regexp.MustCompile(`(?i)^(?:play|give me|make (?:me )?|i want|i'?d like|something|some|a playlist of|playlist of|put on|queue up|build (?:me )?)+\s*`)
	reTailNoise = regexp.MustCompile(`(?i)\s+(?:stuff|music|vibes?|tracks?|songs?|tunes?|playlist|please)\s*$`)
)

func extractSeeds(orig, lower string, now *core.TrackRef, recent []core.TrackRef) ([]string, core.Mode) {
	if m := reJourney.FindStringSubmatch(orig); m != nil {
		// Category journeys are semantic instructions, not artist lookups. Keep
		// the journey mode while leaving retrieval anchors to the semantic path.
		if criteria, _ := CategoryJourney(orig, nil); len(criteria) > 0 {
			return nil, core.ModeJourney
		}
		var seeds []string
		seeds = append(seeds, cleanSeed(m[1]))
		if m[3] != "" {
			seeds = append(seeds, cleanSeed(m[3]))
		}
		seeds = append(seeds, cleanSeed(m[2]))
		if seeds = nonEmpty(seeds); len(seeds) >= 2 {
			return dedupeSeeds(seeds), core.ModeJourney
		}
	}

	if m := reLike.FindStringSubmatch(orig); m != nil {
		if seeds := splitSeeds(m[1]); len(seeds) > 0 {
			return dedupeSeeds(seeds), core.ModeSimilar
		}
	}
	if m := reByArtist.FindStringSubmatch(orig); m != nil {
		if artist := cleanSeed(m[1]); artist != "" {
			return []string{artist}, core.ModeSimilar
		}
	}

	if reThis.MatchString(lower) {
		contextTrack := now
		if contextTrack == nil && len(recent) > 0 {
			contextTrack = &recent[0]
		}
		if contextTrack == nil {
			return nil, core.ModeSimilar
		}
		q := strings.TrimSpace(contextTrack.Artist + " " + contextTrack.Title)
		if q != "" {
			return []string{q}, core.ModeSimilar
		}
	}

	// Short bare prompt: "Daft Punk", "play Radiohead", "Bonobo vibes".
	if fields := strings.Fields(orig); len(fields) > 0 && len(fields) <= 6 {
		s := reTailNoise.ReplaceAllString(reLeadVerb.ReplaceAllString(orig, ""), "")
		if s = cleanSeed(s); s != "" && !looksLikeDirective(s) && !looksLikeCategoryRequest(orig, s) {
			return []string{s}, core.ModeSimilar
		}
	}

	return nil, core.ModeSimilar
}

func looksLikeCategoryRequest(prompt, cleaned string) bool {
	if bareMusicDescription(prompt) != "" {
		return true
	}
	if categoryPrefix(prompt) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if isKnownStyle(cleaned) || isKnownMoodOrActivity(cleaned) {
		return true
	}
	for _, suffix := range []string{" music", " songs", " tracks", " playlist", " vibes"} {
		value := strings.TrimSpace(strings.TrimSuffix(lower, suffix))
		if strings.HasSuffix(lower, suffix) && (isKnownStyle(value) || isKnownMoodOrActivity(value)) {
			return true
		}
	}
	return false
}

// categoryPrefix recognizes a leading category before modifiers and references,
// but not a category word inside an artist name (for example Aesop Rock).
func categoryPrefix(prompt string) bool {
	text := strings.ToLower(strings.TrimSpace(reLeadVerb.ReplaceAllString(prompt, "")))
	for _, value := range append(append([]string(nil), knownStyles...), "relaxing", "sleepy", "upbeat", "mellow", "dreamy", "dark", "joyful", "focus", "study", "workout", "running", "dinner", "sleep", "instrumental", "no vocals", "acoustic") {
		if strings.HasPrefix(text, value) && wordBoundary(text, len(value)) {
			return true
		}
	}
	return false
}

func wordBoundary(text string, offset int) bool {
	if offset <= 0 || offset >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[offset:])
	return !unicode.IsLetter(r) && !unicode.IsNumber(r)
}

func maskReferenceText(prompt string, references []core.IntentReference) string {
	masked := []byte(prompt)
	for _, reference := range references {
		for _, evidence := range reference.Evidence {
			if !evidence.Explicit || evidence.Start < 0 || evidence.End > len(masked) {
				continue
			}
			for i := evidence.Start; i < evidence.End; i++ {
				masked[i] = ' '
			}
		}
	}
	return string(masked)
}

func isKnownMoodOrActivity(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range []string{"relaxing", "sleepy", "upbeat", "mellow", "dreamy", "dark", "joyful", "focus", "study", "workout", "running", "dinner", "sleep"} {
		if value == item {
			return true
		}
	}
	return false
}

func splitSeeds(s string) []string {
	parts := reSeedSplit.Split(s, -1)
	return dedupeSeeds(nonEmpty(mapClean(parts)))
}

func mapClean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, cleanSeed(s))
	}
	return out
}

var reTrailPunct = regexp.MustCompile(`^[\s"'“”‘’]+|[\s"'“”‘’.,;:!?]+$`)

func cleanSeed(s string) string {
	s = reTrailPunct.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.TrimPrefix(s, "the ")
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.TrimSpace(s)
}

func looksLikeDirective(s string) bool {
	switch strings.ToLower(s) {
	case "", "a", "an", "some", "music", "songs", "tracks", "playlist", "stuff", "anything", "something":
		return true
	}
	return false
}

func dedupeSeeds(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		k := strings.ToLower(s)
		if _, dup := seen[k]; dup || s == "" {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// --- creativity / noise / lookback -----------------------------------

var (
	upCreativity = []string{
		"adventurous", "surprising", "surprise me", "surprise", "weird", "deep cut",
		"deep cuts", "obscure", "experimental", "out there", "eclectic", "explore",
		"discover", "rare", "left field", "leftfield", "unexpected", "curveball",
		"bold", "risky", "wild card", "off the beaten", "b-sides", "b sides",
	}
	downCreativity = []string{
		"safe", "familiar", "the hits", "greatest hits", "mainstream", "well known",
		"well-known", "predictable", "comfortable", "comfort", "recognizable",
		"on the nose", "stay close", "don't stray", "dont stray", "crowd pleaser",
		"crowd-pleaser", "nothing weird", "keep it accessible",
	}
	upNoise = []string{
		"unpredictable", "random", "wander", "wandering", "drift", "drifting",
		"meander", "chaotic", "all over the place", "loose", "erratic", "drunk",
		"scattershot", "chaos", "jumpy", "restless", "zigzag",
	}
	downNoise = []string{
		"coherent", "cohesive", "focused", "consistent", "smooth", "tight",
		"flowing", "seamless", "steady", "controlled", "on theme", "no surprises",
	}
)

func countHits(lower string, words []string) int {
	n := 0
	for _, w := range words {
		if strings.Contains(lower, w) {
			n++
		}
	}
	return n
}

func extractCreativity(lower string) float64 {
	score := countHits(lower, upCreativity) - countHits(lower, downCreativity)
	return clampF(core.DefaultCreativity+0.18*float64(score), 0, 1)
}

func extractNoise(lower string) float64 {
	score := 0.22*float64(countHits(lower, upNoise)) - 0.10*float64(countHits(lower, downNoise))
	return clampF(0.10+score, 0, 1)
}

var (
	reShortMemory = regexp.MustCompile(`(?i)\b(?:track by track|one (?:song|track) at a time|no memory|short memory|forget quickly)\b`)
	reLongMemory  = regexp.MustCompile(`(?i)\b(?:stay on theme|keep the thread|very cohesive|hold the vibe|long memory|keep it consistent)\b`)
)

func extractLookback(lower string) int {
	switch {
	case reShortMemory.MatchString(lower):
		return 1
	case reLongMemory.MatchString(lower):
		return 5
	default:
		return core.DefaultLookback
	}
}

// --- constraints -----------------------------------------------------

var reExclude = regexp.MustCompile(`(?i)\b(?:without|no more|nothing by|nothing from|except|but not|skip|avoid|not|excluding)\s+([A-Z][\w.&'’-]*(?:\s+[A-Z][\w.&'’-]*){0,3})`)

func extractArtistExcludes(orig string) []string {
	var out []string
	for _, m := range reExclude.FindAllStringSubmatch(orig, -1) {
		if name := cleanSeed(m[1]); name != "" && !looksLikeDirective(name) && !isKnownStyle(name) && !isKnownMoodOrActivity(name) && !strings.EqualFold(name, "vocals") {
			out = append(out, name)
		}
		if len(out) >= 5 {
			break
		}
	}
	return dedupeSeeds(out)
}

func extractRequiredTracks(prompt string) []core.IntentReference {
	return lexicon.RequiredTracks(prompt)
}

func extractSemanticPreferences(prompt string) core.SemanticPreferences {
	lower := strings.ToLower(prompt)
	var out core.SemanticPreferences
	if value := bareMusicDescription(prompt); value != "" && !isKnownStyle(value) && !isKnownMoodOrActivity(value) && value != "instrumental" && value != "acoustic" {
		out.Genres = append(out.Genres, core.IntentPreference{Value: strings.ToLower(value), Influence: core.InfluencePositive, Explicit: true, Evidence: sourceEvidence(prompt, value, true)})
	}
	add := func(dst *[]core.IntentPreference, value string, influence core.Influence) {
		*dst = append(*dst, core.IntentPreference{
			Value: value, Influence: influence, Explicit: true,
			Evidence: sourceEvidence(prompt, value, true),
		})
	}

	for _, mention := range styleMentions(prompt) {
		out.Styles = append(out.Styles, core.IntentPreference{
			Value: normalizeStyle(mention.value), Influence: mention.influence, Explicit: true,
			Evidence: []core.SourceEvidence{{Text: prompt[mention.start:mention.end], Start: mention.start, End: mention.end, Explicit: true}},
		})
	}
	for _, value := range []string{"relaxing", "sleepy", "upbeat", "mellow", "dreamy", "dark", "joyful", "energetic", "calm", "gentle", "intense"} {
		if strings.Contains(lower, value) {
			influence := core.InfluencePositive
			if strings.Contains(lower, "not "+value) || strings.Contains(lower, "no "+value) {
				influence = core.InfluenceNegative
			}
			add(&out.Moods, value, influence)
		}
	}
	for _, value := range []string{"instrumental", "acoustic", "synthesizer", "guitar", "piano"} {
		if strings.Contains(lower, value) {
			add(&out.Instrumentation, value, core.InfluencePositive)
		}
	}
	for _, value := range []string{"microdetail", "a deep groove", "deep groove", "occasional sparkle", "sparkle", "relaxing but not sleepy"} {
		if strings.Contains(lower, value) {
			add(&out.TextureDescriptions, value, core.InfluencePositive)
		}
	}
	if strings.Contains(lower, "instrumental") || strings.Contains(lower, "no vocals") {
		value := "instrumental"
		if strings.Contains(lower, "no vocals") {
			value = "no vocals"
		}
		preference := core.IntentPreference{Value: value, Influence: core.InfluencePositive, Explicit: true, Evidence: sourceEvidence(prompt, value, true)}
		out.VocalPreference = &preference
	} else if strings.Contains(lower, "vocals") || strings.Contains(lower, "vocal") {
		preference := core.IntentPreference{Value: "vocals", Influence: core.InfluencePositive, Explicit: true, Evidence: sourceEvidence(prompt, "vocal", true)}
		out.VocalPreference = &preference
	}
	// Preserve an open-vocabulary description before an explicit reference,
	// e.g. "<musical description> music like <artist>". Reference text is
	// already masked; this is a requested style, not an artist classification.
	if match := reLeadingMusicDescription.FindStringSubmatch(reLeadVerb.ReplaceAllString(prompt, "")); match != nil {
		value := strings.TrimSpace(match[1])
		covered := isKnownStyle(value) || isKnownMoodOrActivity(value)
		for _, preference := range out.Instrumentation {
			covered = covered || strings.EqualFold(value, preference.Value)
		}
		if !covered && !reDescriptionNegation.MatchString(value) {
			out.Styles = append([]core.IntentPreference{{Value: value, Influence: core.InfluencePositive, Explicit: true, Evidence: sourceEvidence(prompt, value, true)}}, out.Styles...)
		}
	}
	return out
}

var reLeadingMusicDescription = regexp.MustCompile(`(?i)^\s*(.+?)\s+music\s+(?:like|by|similar to|inspired by)\b`)
var reBareMusicDescription = regexp.MustCompile(`(?i)^\s*([^,;.!?"“”]+?)\s+music\s*[,.;!?]?\s*$`)
var reDescriptionNegation = regexp.MustCompile(`(?i)\b(?:no|not|without|except)\b`)

func bareMusicDescription(prompt string) string {
	if match := reBareMusicDescription.FindStringSubmatch(reLeadVerb.ReplaceAllString(prompt, "")); match != nil && !reDescriptionNegation.MatchString(match[1]) {
		return strings.TrimSpace(match[1])
	}
	return ""
}

var knownStyles = []string{
	"ambient electronic", "ambient electronica", "rock & roll", "rock and roll", "abstract drone",
	"electronic", "electronica", "ambient", "techno", "jazz", "folk", "rock", "drone",
}

func isKnownStyle(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, " music")
	for _, style := range knownStyles {
		if value == style {
			return true
		}
	}
	return false
}

type styleMention struct {
	value      string
	start, end int
	influence  core.Influence
}

var reStyleMention = regexp.MustCompile(`(?i)ambient electronica|ambient electronic|rock\s*(?:&|and)\s*roll|abstract drone|electronica|electronic|ambient|techno|jazz|folk|rock|drone`)
var reStyleNegation = regexp.MustCompile(`(?i)\b(?:no|not|without|must not include)\s+$`)

func styleMentions(prompt string) []styleMention {
	var result []styleMention
	for _, loc := range reStyleMention.FindAllStringIndex(prompt, -1) {
		left, _ := utf8.DecodeLastRuneInString(prompt[:loc[0]])
		if loc[0] > 0 && (unicode.IsLetter(left) || unicode.IsNumber(left)) || !wordBoundary(prompt, loc[1]) {
			continue
		}
		influence := core.InfluencePositive
		if reStyleNegation.MatchString(prompt[:loc[0]]) {
			influence = core.InfluenceNegative
		}
		result = append(result, styleMention{value: prompt[loc[0]:loc[1]], start: loc[0], end: loc[1], influence: influence})
	}
	return result
}

func extractEssentialCriteria(prompt string, preferences core.SemanticPreferences) []core.MusicalCriterion {
	lower := strings.ToLower(prompt)
	if criteria, _ := CategoryJourney(prompt, nil); len(criteria) > 0 {
		return criteria
	}
	for _, preference := range preferences.Genres {
		if preference.Influence == core.InfluencePositive {
			return []core.MusicalCriterion{{Kind: "genre", Value: preference.Value, Scope: "playlist", Evidence: preference.Evidence}}
		}
	}

	// A category-led request makes its primary genre essential. Qualifiers such
	// as "with some rock influence" remain soft, preserving deliberate hybrids.
	categoryLed := categoryPrefix(prompt) || strings.Contains(lower, " music") || strings.Contains(lower, " playlist") || strings.Contains(lower, " tracks") || strings.Contains(lower, " songs")
	if !categoryLed {
		return nil
	}
	for _, preference := range preferences.Styles {
		if preference.Influence != core.InfluencePositive || isInfluenceQualifier(lower, preference.Value) {
			continue
		}
		return []core.MusicalCriterion{{Kind: "style", Value: normalizeStyle(preference.Value), Scope: "playlist", Evidence: preference.Evidence}}
	}
	return nil
}

func isInfluenceQualifier(lower, style string) bool {
	return strings.Contains(lower, "some "+style+" influence") ||
		strings.Contains(lower, style+" influence") || strings.Contains(lower, "touch of "+style)
}

func normalizeStyle(value string) string {
	value = strings.ToLower(strings.Join(strings.Fields(value), " "))
	value = strings.TrimSuffix(value, " music")
	return core.CanonicalStyle(value)
}

func extractUnsupportedRequirements(
	prompt string,
	unsupported []core.UnsupportedRequirement,
	constraints []core.HardConstraint,
) ([]core.UnsupportedRequirement, []core.HardConstraint) {
	if index := strings.Index(strings.ToLower(prompt), "no vocals"); index >= 0 {
		span := prompt[index : index+len("no vocals")]
		evidence := []core.SourceEvidence{{Text: span, Start: index, End: index + len(span), Explicit: true}}
		constraints = append(constraints, core.HardConstraint{Kind: "exclude_vocals", Value: "vocals", Supported: false, Evidence: evidence})
		unsupported = append(unsupported, core.UnsupportedRequirement{Text: span, Reason: "requires a compatible semantic sidecar with vocal evidence", Evidence: evidence})
	}
	for _, mention := range styleMentions(prompt) {
		if mention.influence != core.InfluenceNegative {
			continue
		}
		prefix := reStyleNegation.FindStringIndex(prompt[:mention.start])
		span := prompt[prefix[0]:mention.end]
		value := normalizeStyle(mention.value)
		evidence := []core.SourceEvidence{{Text: span, Start: prefix[0], End: mention.end, Explicit: true}}
		constraints = append(constraints, core.HardConstraint{
			Kind: "exclude_style", Value: value, Supported: false, Evidence: evidence,
		})
		unsupported = append(unsupported, core.UnsupportedRequirement{
			Text: span, Reason: "the current catalog has no style labels", Evidence: evidence,
		})
	}
	return unsupported, constraints
}

// --- summary -------------------------------------------------------

func summarize(m core.MusicIntent) string {
	var parts []string
	if len(m.Seeds.Queries) > 0 {
		verb := "seeds "
		if m.Mode == core.ModeJourney {
			verb = "journey through "
		}
		parts = append(parts, verb+strings.Join(m.Seeds.Queries, " → "))
	}
	parts = append(parts, fmt.Sprintf("%d tracks", m.Count))
	switch {
	case m.Creativity >= 0.66:
		parts = append(parts, "leaning adventurous")
	case m.Creativity <= 0.34:
		parts = append(parts, "leaning familiar")
	}
	switch {
	case m.Noise >= 0.40:
		parts = append(parts, "wandering")
	case m.Noise <= 0.05:
		parts = append(parts, "tight")
	}
	if len(m.Constraints.ArtistsExclude) > 0 {
		parts = append(parts, "excluding "+strings.Join(m.Constraints.ArtistsExclude, ", "))
	}
	s := strings.Join(parts, " · ")
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var _ ports.IntentParser = (*Parser)(nil)
