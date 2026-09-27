package lexicon

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var (
	genreCoverageCue    = regexp.MustCompile(`(?i)\b(?:mix(?:es|ing)?|blend(?:s|ing)?|combin(?:e|es|ing|ation)|span(?:s|ning)?)(?:\s+of)?(?:\s+(?:both|the genres?))?\s+`)
	genreListJoin       = regexp.MustCompile(`(?i)^\s*(?:,\s*(?:and|or)?|and|or|&|plus)\s*$`)
	perTrackPrefix      = regexp.MustCompile(`(?i)\b(?:(?:each|every|per)(?:\s+(?:of|the|these|those|single|individual|[0-9]+))*\s+(?:songs?|tracks?|recordings?)|all(?:\s+[\p{L}\p{N}-]+){0,3}\s+(?:songs|tracks|recordings)|(?:songs|tracks|recordings)\s+(?:that\s+)?each)\b`)
	perTrackSuffix      = regexp.MustCompile(`(?i)\b(?:in|within|on|for)\s+(?:each|every|all(?:\s+the)?)\s+(?:songs?|tracks?|recordings?)\b`)
	perTrackAllGenres   = regexp.MustCompile(`(?i)\b(?:each|every)\s+(?:song|track|recording)\b[^.;!?\n]{0,60}\b(?:both|all)(?:\s+(?:(?:of\s+)?(?:the|these|those)\s+)?(?:genres?|styles?)\b|\s+(?:of\s+)?(?:them|these|those)\b|[.;!?\n]|$)`)
	negativeCoverageCue = regexp.MustCompile(`(?i)\b(?:don't|don’t|do not|never|no|avoid|without|not)(?:\s+(?:make|making|a|any|the|playlist|that|is)){0,6}\s*$`)
)

// WithGenreCoverage annotates explicit genre lists while source text is being
// interpreted. It is never called during history normalization or ranking.
// Reapplying after recognition removes groups invalidated by protected names.
func WithGenreCoverage(source core.IntentTranslation) core.IntentTranslation {
	out := source.Clone()
	var genres []int
	for i := range out.Atoms {
		a := &out.Atoms[i]
		a.CoverageGroup = ""
		if (a.Kind == "genre" || a.Kind == "style") && a.Scope == "playlist" && a.Polarity == "positive" && (a.Strength == "essential" || a.Strength == "required") && len(a.Evidence) == 1 {
			e := a.Evidence[0]
			if e.Explicit && e.Start >= 0 && e.End > e.Start && e.End <= len(out.OriginalText) && out.OriginalText[e.Start:e.End] == e.Text {
				genres = append(genres, i)
			}
		}
	}
	kept := out.Markers[:0]
	for _, marker := range out.Markers {
		if marker.Kind != "genre_coverage" {
			kept = append(kept, marker)
		}
	}
	out.Markers = kept
	sort.SliceStable(genres, func(i, j int) bool {
		return out.Atoms[genres[i]].Evidence[0].Start < out.Atoms[genres[j]].Evidence[0].Start
	})
	prompt := out.OriginalText
	for _, cue := range genreCoverageCue.FindAllStringIndex(prompt, -1) {
		if insideQuoted(prompt, cue[0], cue[1]) || negativePrefix.MatchString(prompt[:cue[0]]) || negativeCoverageCue.MatchString(prompt[:cue[0]]) {
			continue
		}
		start := strings.LastIndexAny(prompt[:cue[0]], ".!?;\n") + 1
		end := len(prompt)
		if boundary := strings.IndexAny(prompt[cue[1]:], ".!?;\n"); boundary >= 0 {
			end = cue[1] + boundary
		}
		if perTrackPrefix.MatchString(prompt[start:cue[0]]) {
			continue
		}
		var members []int
		last := cue[1]
		for _, index := range genres {
			e := out.Atoms[index].Evidence[0]
			if e.Start < cue[1] || e.End > end {
				continue
			}
			if e.Start < last {
				continue // Recognition can retain overlapping genre/style spans.
			}
			gap := genreCoverageGap(out, last, e.Start)
			if len(members) == 0 && strings.TrimSpace(gap) != "" || len(members) > 0 && !genreListJoin.MatchString(gap) {
				break
			}
			members = append(members, index)
			last = e.End
		}
		if len(members) < 2 || len(members) > core.MaxCount || perTrackSuffix.MatchString(prompt[last:end]) || perTrackAllGenres.MatchString(prompt[last:]) {
			continue
		}
		group := fmt.Sprintf("coverage:%d", cue[0])
		for _, index := range members {
			out.Atoms[index].CoverageGroup = group
		}
		out.Markers = append(out.Markers, core.SyntacticMarker{Kind: "genre_coverage", Text: prompt[cue[0]:last], Start: cue[0], End: last})
	}
	sort.SliceStable(out.Markers, func(i, j int) bool { return out.Markers[i].Start < out.Markers[j].Start })
	return out
}

// A recognized gentle/warm/etc. modifier does not break an explicit list.
// Keep its atom intact: it remains a separate description, never a genre alternative.
func genreCoverageGap(source core.IntentTranslation, start, end int) string {
	gap := []byte(source.OriginalText[start:end])
	for _, a := range source.Atoms {
		if a.Polarity != "positive" || (a.Kind != "mood" && a.Kind != "texture") {
			continue
		}
		for _, e := range a.Evidence {
			if e.Explicit && e.Start >= start && e.End <= end && e.End > e.Start && source.OriginalText[e.Start:e.End] == e.Text {
				for i := e.Start - start; i < e.End-start; i++ {
					gap[i] = ' '
				}
			}
		}
	}
	return string(gap)
}
