// Package rules is a dependency-free, always-available IntentParser: it turns a
// natural-language prompt into a core.MusicIntent with regexes and keyword
// tables. It is the fallback when no local LLM is loaded, and the deterministic
// baseline the llama backend is measured against.
package rules

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Parser implements ports.IntentParser.
type Parser struct{}

// New returns a rules parser.
func New() *Parser { return &Parser{} }

// Info implements ports.IntentParser.
func (*Parser) Info() ports.ParserInfo {
	return ports.ParserInfo{Name: "rules", Backend: "rules", Version: "rules/v2", Ready: true, ContractVersion: core.CurrentIntentVersion, Evidence: true}
}

// Parse implements ports.IntentParser. It never returns an error; an unparsable
// prompt yields an intent with no seeds, which the caller surfaces to the user.
func (*Parser) Parse(_ context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	prompt := strings.TrimSpace(in.Prompt)
	lower := strings.ToLower(prompt)

	intent := core.MusicIntent{
		Version: core.CurrentIntentVersion,
		Controls: core.IntentControls{
			TotalTrackCount:      core.DefaultCount,
			AudioWeight:          core.DefaultCreativity,
			CooccurrenceWeight:   1 - core.DefaultCreativity,
			Discovery:            0.10,
			ArtistDiversity:      0.7,
			TransitionSmoothness: float64(core.DefaultLookback-1) / 9,
		},
	}

	seeds, mode := extractSeeds(prompt, lower, in.NowPlaying, in.RecentTracks)
	for _, seed := range seeds {
		ref := typedReference(prompt, seed, core.ReferenceArtist, core.InfluencePositive)
		intent.References = append(intent.References, ref)
		if mode == core.ModeJourney {
			intent.Journey.Waypoints = append(intent.Journey.Waypoints, ref)
		}
	}
	intent.Mode = mode

	if n, ok := extractCount(lower); ok {
		intent.Controls.TotalTrackCount = n
	}
	intent.Controls.AudioWeight = extractCreativity(lower)
	intent.Controls.CooccurrenceWeight = 1 - intent.Controls.AudioWeight
	intent.Controls.Discovery = extractNoise(lower)
	lookback := extractLookback(lower)
	intent.Controls.TransitionSmoothness = float64(lookback-1) / 9

	for _, artist := range extractArtistExcludes(prompt) {
		evidence := sourceEvidence(prompt, artist, true)
		intent.References = append(intent.References, core.IntentReference{
			Kind: core.ReferenceArtist, Query: artist, Influence: core.InfluenceNegative, Evidence: evidence,
		})
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: "exclude_artist", Value: artist, Supported: true, Evidence: evidence,
		})
	}
	if !reAllowRepeat.MatchString(lower) {
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{
			Kind: "no_back_to_back_artist", Value: "true", Supported: true,
		})
	} else {
		intent.Controls.ArtistDiversity = 0.3
	}
	intent.RequiredTracks = extractRequiredTracks(prompt)
	intent.Preferences = extractSemanticPreferences(prompt)
	intent.Unsupported, intent.HardConstraints = extractUnsupportedRequirements(prompt, intent.Unsupported, intent.HardConstraints)
	intent.Journey.EnergyTrajectory = extractEnergyTrajectory(lower)

	out := intent.Normalized()
	out.Mode = mode // Normalized() would flip an unset mode to journey for >=2 seeds
	out.NotesForUser = summarize(out)
	return out, nil
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
	return core.IntentReference{
		Kind: kind, Query: value, Influence: influence, Evidence: sourceEvidence(prompt, value, true),
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
	reThis      = regexp.MustCompile(`(?i)\b(this one|this track|this song|this|current(?:ly playing)?|what'?s playing|now playing|keep (?:it )?going|keep playing|more of this|in this vein|same vibe|same energy)\b`)
	reSeedSplit = regexp.MustCompile(`(?i)\s*(?:,|\band\b|&|\+|\bwith\b)\s*`)
	reLeadVerb  = regexp.MustCompile(`(?i)^(?:play|give me|make (?:me )?|i want|i'?d like|something|some|a playlist of|playlist of|put on|queue up|build (?:me )?)+\s*`)
	reTailNoise = regexp.MustCompile(`(?i)\s+(?:stuff|music|vibes?|tracks?|songs?|tunes?|playlist|please)\s*$`)
)

func extractSeeds(orig, lower string, now *core.TrackRef, recent []core.TrackRef) ([]string, core.Mode) {
	if m := reJourney.FindStringSubmatch(orig); m != nil {
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
		if s = cleanSeed(s); s != "" && !looksLikeDirective(s) {
			return []string{s}, core.ModeSimilar
		}
	}

	return nil, core.ModeSimilar
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

// --- count -------------------------------------------------------------

var reDigitsCount = regexp.MustCompile(`(?i)\b(\d{1,3})\s*(?:songs?|tracks?|tunes?|of them|long)\b`)

var wordCounts = map[string]int{
	"ten": 10, "twelve": 12, "fifteen": 15, "sixteen": 16, "eighteen": 18,
	"twenty": 20, "twenty-five": 25, "twenty five": 25, "thirty": 30,
	"forty": 40, "fifty": 50, "sixty": 60, "hundred": 100,
	"a dozen": 12, "half a dozen": 6, "a handful": 8, "a few": 5,
}

func extractCount(lower string) (int, bool) {
	if m := reDigitsCount.FindStringSubmatch(lower); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
	}
	for phrase, n := range wordCounts {
		if strings.Contains(lower, phrase+" song") || strings.Contains(lower, phrase+" track") ||
			strings.Contains(lower, phrase+" tune") || strings.Contains(lower, phrase+" of them") {
			return n, true
		}
	}
	if strings.Contains(lower, "a dozen") {
		return 12, true
	}
	return 0, false
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

var (
	reExclude     = regexp.MustCompile(`(?i)\b(?:without|no more|nothing by|nothing from|except|but not|skip|avoid|not|excluding)\s+([A-Z][\w.&'’-]*(?:\s+[A-Z][\w.&'’-]*){0,3})`)
	reAllowRepeat = regexp.MustCompile(`(?i)\b(?:same artist (?:is )?ok|repeat artists?|let artists? repeat|same-artist repeats? (?:are )?fine|allow (?:artist )?repeats?)\b`)
)

func extractArtistExcludes(orig string) []string {
	var out []string
	for _, m := range reExclude.FindAllStringSubmatch(orig, -1) {
		if name := cleanSeed(m[1]); name != "" && !looksLikeDirective(name) {
			out = append(out, name)
		}
		if len(out) >= 5 {
			break
		}
	}
	return dedupeSeeds(out)
}

var reRequiredTrack = regexp.MustCompile(`(?i)\b(?:must include|include|make sure to include)\s+["“]?([^,;"”]+\s+-\s+[^,;"”]+)`)

func extractRequiredTracks(prompt string) []core.IntentReference {
	var out []core.IntentReference
	for _, match := range reRequiredTrack.FindAllStringSubmatch(prompt, -1) {
		value := strings.TrimSpace(match[1])
		if value == "" {
			continue
		}
		out = append(out, typedReference(prompt, value, core.ReferenceTrack, core.InfluencePositive))
	}
	return out
}

func extractSemanticPreferences(prompt string) core.SemanticPreferences {
	lower := strings.ToLower(prompt)
	var out core.SemanticPreferences
	add := func(dst *[]core.IntentPreference, value string, influence core.Influence) {
		*dst = append(*dst, core.IntentPreference{
			Value: value, Influence: influence, Explicit: true,
			Evidence: sourceEvidence(prompt, value, true),
		})
	}

	for _, value := range []string{"ambient electronic", "abstract drone", "electronic", "ambient", "jazz", "techno", "folk"} {
		if strings.Contains(lower, value) {
			influence := core.InfluencePositive
			if strings.Contains(lower, "no "+value) || strings.Contains(lower, "not "+value) {
				influence = core.InfluenceNegative
			}
			add(&out.Styles, value, influence)
		}
	}
	for _, value := range []string{"relaxing", "sleepy", "upbeat", "mellow", "dreamy", "dark", "joyful"} {
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
	return out
}

var reUnsupportedStyle = regexp.MustCompile(`(?i)\b(?:no|without|must not include)\s+(abstract drone|drone|ambient|techno|jazz)\b`)

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
	for _, match := range reUnsupportedStyle.FindAllStringSubmatchIndex(prompt, -1) {
		span := prompt[match[0]:match[1]]
		value := prompt[match[2]:match[3]]
		evidence := []core.SourceEvidence{{Text: span, Start: match[0], End: match[1], Explicit: true}}
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
