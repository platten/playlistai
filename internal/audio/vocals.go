package audio

import (
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// This is zero-shot class comparison, not a calibrated probability or proof
// about unobserved parts of a recording. Version changes invalidate assessments.
const VocalPolicyVersion = "clap-preview-vocal-contrast/v1"

// An engineering abstention band in cosine units, not a calibrated threshold.
// Small leads do not establish useful separation between these descriptions.
const vocalSeparation = 0.02

var instrumentalPrompts = []string{
	"An instrumental music recording.",
	"This is instrumental music.",
	"A song played only on instruments.",
}
var vocalPrompts = []string{
	"A song with a person singing.",
	"Music with vocals.",
	"Music with a male singer.",
	"Music with a female singer.",
	"Music with a choir singing.",
	"Music with spoken words or rap.",
	"Music with humming or wordless singing.",
}
var otherPrompts = []string{"Silence.", "Noise without music."}

func instrumentalClause(c core.AudioClause) bool {
	if c.Strength == "preferred" || c.Degree == "mostly" || c.Degree == "reduced" {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(c.Text))
	return c.Kind == "vocal" && c.Negative && v == "vocals" ||
		!c.Negative && (v == "instrumental" || v == "no vocals")
}

func requiredInstrumentalScreen(clauses []core.AudioClause) bool {
	for _, c := range clauses {
		if (c.Strict || c.Essential) && instrumentalClause(c) {
			return true
		}
	}
	return false
}

// Every segment must prefer an instrumental description over every vocal and
// non-musical description. Ties, invalid vectors and missing evidence abstain.
func (s *Session) instrumentalEvidence(record core.AudioAnalysis) (core.EvidenceState, float64) {
	if len(record.Segments) == 0 || record.Identity.Status != core.ResolutionResolved {
		return core.EvidenceUnknown, 0
	}
	groups := [][][]float32{{}, {}, {}}
	for i, prompts := range [][]string{instrumentalPrompts, vocalPrompts, otherPrompts} {
		for _, prompt := range prompts {
			vector, ok := s.queries[prompt]
			if !ok {
				var err error
				vector, err = s.service.Analyzer.EmbedText(s.ctx, prompt)
				if err != nil || !validVector(vector, record.Model.Dimension) {
					return core.EvidenceUnknown, 0
				}
				s.queries[prompt] = vector
			}
			groups[i] = append(groups[i], vector)
		}
	}
	state, strongestVocal := core.EvidenceMatch, -1.0
	for _, segment := range record.Segments {
		if segment.EndSeconds <= segment.StartSeconds || !validVector(segment.Embedding, record.Model.Dimension) {
			return core.EvidenceUnknown, 0
		}
		best := []float64{-1, -1, -1}
		for i, vectors := range groups {
			for _, query := range vectors {
				score := segmentSimilarity(query, []core.AudioSegment{segment}, false)
				best[i] = max(best[i], score)
			}
		}
		strongestVocal = max(strongestVocal, best[1])
		switch {
		case best[1] > best[0]+1e-6 && best[1] > best[2]+1e-6:
			return core.EvidenceMismatch, strongestVocal
		case best[0] <= 0 || best[0]-best[2] <= vocalSeparation || math.Abs(best[0]-best[1]) <= vocalSeparation:
			state = core.EvidenceUnknown
		}
	}
	return state, strongestVocal
}

func (s *Session) SupportsConstraint(kind string) bool {
	return kind == "exclude_vocals" || kind == "require_instrumental" ||
		s.service.Policy.Valid() && (kind == "exclude_style" || kind == "require_style")
}
