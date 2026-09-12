package multichannel

import (
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

var enhancedPhrasePattern = regexp.MustCompile(`\b(strong sub-bass|strong subbass|bass-heavy|bass heavy|deep bass|more bass|strong bass|lots of transients|sharp attacks|percussive|big dynamic swings|wide dynamics|compressed dynamics|bright sound|dark sound|darker)\b`)
var enhancedQuotedPattern = regexp.MustCompile(`"[^"\n]*"`)
var enhancedNegationPattern = regexp.MustCompile(`\b(no|not|less|without|avoid)(\s+[a-z]+){0,2}\s*$`)
var enhancedAdditivePattern = regexp.MustCompile(`\bnot (?:only|just|merely)\s*$`)

// This Enhanced-only vocabulary extraction leaves the saved intent and existing
// parser modes untouched. It recognizes narrow production phrases, not arbitrary
// mood adjectives or musical categories. Quoted/named references are excluded.
func enhancedClauses(intent core.MusicIntent) []core.AudioClause {
	clauses := audio.Clauses(intent)
	description := strings.ToLower(intent.OriginalDescription)
	if intent.Translation != nil {
		description = "" // reconciled occurrence-aware extraction owns new requests
	}
	// Raw language does not carry reliable stage boundaries. Preserve scoped
	// intent by using only its explicitly global texture clauses in journeys.
	if intent.Mode == core.ModeJourney {
		description = ""
	}
	for _, clause := range clauses {
		if clause.Scope != "" && clause.Scope != "playlist" {
			description = ""
			break
		}
	}
	description = enhancedQuotedPattern.ReplaceAllString(description, " ")
	for _, reference := range intent.References {
		if reference.Resolution != nil && reference.Resolution.Selected != nil {
			if query := strings.TrimSpace(strings.ToLower(reference.Query)); len(query) > 2 {
				description = strings.ReplaceAll(description, query, " ")
			}
			for _, name := range []string{reference.Resolution.Selected.Artist, reference.Resolution.Selected.Title} {
				if len(name) > 2 {
					description = strings.ReplaceAll(description, strings.ToLower(name), " ")
				}
			}
		}
	}
	for _, location := range enhancedPhrasePattern.FindAllStringIndex(description, -1) {
		prefix := description[:location[0]]
		if last := strings.LastIndexAny(prefix, ".,;:!?"); last >= 0 {
			prefix = prefix[last+1:]
		}
		clauses = append(clauses, core.AudioClause{Kind: "texture", Text: description[location[0]:location[1]], Scope: "playlist", Negative: enhancedNegationPattern.MatchString(prefix) && !enhancedAdditivePattern.MatchString(prefix)})
	}
	// Alias duplication must not give one measured axis extra voting weight.
	seen := map[core.AudioClause]bool{}
	result := make([]core.AudioClause, 0, len(clauses))
	for _, clause := range clauses {
		if clause.Kind != "texture" && clause.Kind != "description" {
			continue
		}
		if clause.Scope != "" && clause.Scope != "playlist" {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(clause.Text))
		if concept, ok := musicconcepts.Find(clause.Kind, clause.Text); ok && (intent.Translation != nil || clause.ConceptID != "") {
			if len(concept.Providers.DSP) != 1 {
				continue
			}
			axis, direction, _ := strings.Cut(concept.Providers.DSP[0], ":")
			switch axis {
			case "bass_energy_ratio":
				text = "bass heavy"
			case "subbass_energy_ratio":
				text = "strong sub-bass"
			case "spectral_centroid_hz":
				text = "bright"
			case "short_window_rms_spread_db":
				text = "dynamic"
			case "transients":
				text = "percussive"
			default:
				continue
			}
			if direction == "negative" {
				clause.Negative = !clause.Negative
			}
		}
		switch text {
		case "deep bass", "more bass", "strong bass", "bass-heavy", "bass heavy":
			text = "bass heavy"
		case "strong sub-bass", "strong subbass":
			text = "strong sub-bass"
		case "bright", "bright sound":
			text = "bright"
		case "dark", "dark sound", "darker":
			text = "bright"
			clause.Negative = !clause.Negative
		case "dynamic", "wide dynamics", "big dynamic swings":
			text = "dynamic"
		case "compressed", "compressed dynamics":
			text = "dynamic"
			clause.Negative = !clause.Negative
		case "lots of transients", "sharp attacks", "percussive":
			text = "percussive"
		default:
			continue
		}
		clause = core.AudioClause{Kind: "texture", Text: text, Negative: clause.Negative, Scope: "playlist", Degree: clause.Degree}
		if !seen[clause] {
			seen[clause] = true
			result = append(result, clause)
		}
	}
	return result
}
