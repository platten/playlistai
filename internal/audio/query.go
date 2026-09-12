package audio

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// QueryPolicyVersion separates new request assessments from raw-clause scores.
// Audio embeddings remain reusable; no musical-fit threshold is calibrated by
// this policy. Each clause retains its own polarity, scope and evidence.
const QueryPolicyVersion = "typed-clause-ensemble/v1"

// ClauseQueries keeps the original wording and adds one short musical context.
// It introduces no synonyms or inferred attributes. Long or already sentence-
// shaped descriptions keep their original text; the tokenizer enforces its own
// final token limit. Strict checks and calibrated policies use their old queries.
func ClauseQueries(clause core.AudioClause) []string {
	text := strings.TrimSpace(clause.Text)
	if text == "" {
		return nil
	}
	queries := []string{text}
	if clause.Strict || len(text) > 160 || len(strings.Fields(text)) > 24 {
		return queries
	}
	var caption string
	switch clause.Kind {
	case "genre", "style":
		caption = "Music in the style of " + text + "."
	case "mood":
		caption = "Music with a " + text + " mood."
	case "instrumentation":
		caption = "Music featuring " + text + "."
	case "texture":
		caption = "Music with " + text + "."
	case "vocal":
		caption = "Music with " + text + "."
	}
	if caption != "" && !strings.EqualFold(caption, text) {
		queries = append(queries, caption)
	}
	return queries
}

func (s *Session) clauseQuery(clause core.AudioClause) []float32 {
	texts := []string{clause.Text}
	if s.typedQueries() {
		texts = ClauseQueries(clause)
	}
	if len(texts) == 0 {
		return nil
	}
	var sum []float32
	for _, text := range texts {
		query, cached := s.queries[text]
		if !cached {
			vector, err := s.service.Analyzer.EmbedText(s.ctx, text)
			if err == nil && validVector(vector, s.snapshot.Model.Dimension) {
				query = vector
			}
			s.queries[text] = query // failed variants are not retried for every track
		}
		if len(query) == 0 {
			return nil
		} // no silent change of query policy on failure
		if sum == nil {
			sum = make([]float32, len(query))
		}
		for i, x := range query {
			sum[i] += x / float32(len(texts))
		}
	}
	// Do not normalize the mean: its dot product is the mean cosine across the
	// fixed variants, including their disagreement, on the original score scale.
	return sum
}

func (s *Session) typedQueries() bool {
	return !s.service.Policy.Valid() && s.intent.VerificationPolicy == core.BestAvailable
}
