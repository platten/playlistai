package libraryannotate

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// LiteralExtractor admits only short explicit statements that name both this
// recording and performer. It cannot turn general page keywords, album prose,
// absent adjectives or an LLM judgment into a musical annotation.
type LiteralExtractor struct{ Criteria []core.MusicalCriterion }

func (e LiteralExtractor) ExtractRecordingClaims(ctx context.Context, s ports.RecordingSource) ([]core.RecordingClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []core.RecordingClaim
	quotedWords := 0
	for _, sentence := range strings.FieldsFunc(s.Text, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '\n' }) {
		sentence = strings.TrimSpace(sentence)
		words := len(strings.Fields(sentence))
		if words == 0 || quotedWords+words > 25 {
			continue
		}
		subject := core.NormalizeIdentityPart(s.Track.Ref.Title + " by " + s.Track.Ref.Artist)
		normalized := core.NormalizeIdentityPart(sentence)
		if !strings.HasPrefix(normalized, subject+" ") {
			continue
		}
		predicate := strings.TrimPrefix(normalized, subject+" ")
		for _, criterion := range e.Criteria {
			value := core.NormalizeIdentityPart(criterion.Value)
			state := core.EvidenceUnknown
			for _, verb := range []string{"features ", "uses ", "contains ", "is "} {
				if predicate == verb+value {
					state = core.EvidenceMatch
				}
			}
			for _, verb := range []string{"does not feature ", "does not use ", "does not contain ", "is not "} {
				if predicate == verb+value {
					state = core.EvidenceMismatch
				}
			}
			if state == core.EvidenceUnknown || quotedWords+words > 25 {
				continue
			}
			out = append(out, core.RecordingClaim{Kind: criterion.Kind, Value: criterion.Value, State: state, Scope: "recording", EntityID: s.Track.RecordingID, RecordingID: s.Track.RecordingID, Source: s.Source, Locator: sentence, Method: "quoted_statement", ExtractorVersion: Version + "/literal-v1"})
			quotedWords += words
		}
	}
	return out, nil
}

// QuoteBoundedExtractor retains at most 25 quoted words from a source per call.
// Model extraction remains a quoted_statement hint, never a listening label.
type QuoteBoundedExtractor struct {
	Extractor ports.RecordingSourceExtractor
}

func (e QuoteBoundedExtractor) ExtractRecordingClaims(ctx context.Context, s ports.RecordingSource) ([]core.RecordingClaim, error) {
	claims, err := e.Extractor.ExtractRecordingClaims(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []core.RecordingClaim
	words := 0
	for _, claim := range claims {
		count := len(strings.Fields(claim.Locator))
		if count == 0 || words+count > 25 || !strings.Contains(s.Text, claim.Locator) {
			continue
		}
		words += count
		claim.Method = "quoted_statement"
		out = append(out, claim)
	}
	return out, nil
}
