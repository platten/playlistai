package audio

import (
	"sort"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// QueryPolicyVersion separates new request assessments from raw-clause scores.
// Audio embeddings remain reusable; no musical-fit threshold is calibrated by
// this policy. Each clause retains its own polarity, scope and evidence.
const QueryPolicyVersion = "typed-clause-ensemble/v2+" + musicconcepts.Version

// ClauseQueries translates exact reviewed aliases and adds one short musical
// context. Parent genres are never substituted. Unknown or long descriptions
// keep their wording; strict checks and calibrated policies keep raw queries.
func ClauseQueries(clause core.AudioClause) []string {
	text := strings.TrimSpace(clause.Text)
	if text == "" {
		return nil
	}
	if clause.Strict || len(text) > 160 || len(strings.Fields(text)) > 24 {
		return []string{text}
	}
	text = musicconcepts.Canonical(clause.Kind, text)
	queries := []string{text}
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
	if concept, ok := musicconcepts.Find(clause.Kind, text); ok && len(concept.Providers.CLAP) > 0 {
		caption = concept.Providers.CLAP[0]
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

// applyTypedScores gives each facet equal weight, alternatives within an OR
// group their best score, and each journey stage its own positives/negatives.
// The best net stage supplies both scores so one stage cannot borrow another's
// low penalty. These are ranking choices, never calibrated match probabilities.
func applyTypedScores(candidate *core.Candidate, assessment core.AudioAssessment) {
	type scoreGroup struct {
		scope, kind string
		negative    bool
		score       float64
	}
	groups := map[string]scoreGroup{}
	groupSigns := map[string]uint8{}
	groupKey := func(c core.AudioClause) string {
		scope := c.Scope
		if scope == "" {
			scope = "playlist"
		}
		return scope + "\x00" + c.Group
	}
	for _, a := range assessment.Clauses {
		if a.Clause.Group == "" {
			continue
		}
		bit := uint8(1)
		if a.Clause.Negative {
			bit = 2
		}
		groupSigns[groupKey(a.Clause)] |= bit
	}
	for i, a := range assessment.Clauses {
		if !a.ScoreAvailable && a.State == core.EvidenceUnknown {
			continue
		}
		c := a.Clause
		scope := c.Scope
		if scope == "" {
			scope = "playlist"
		}
		kind := c.Kind
		if kind == "style" {
			kind = "genre"
		}
		group := c.Group
		negative := c.Negative
		if group == "" {
			// Each ungrouped trait is an independent preference.
			group = "\x00" + strconv.Itoa(i)
		} else {
			// One OR group is one fixed facet even when alternatives cross
			// kinds (piano OR ambient). Its winner cannot change facet weights.
			kind = "\x00or:" + group
		}
		key := scope + "\x00" + kind + "\x00" + group
		score := a.Score
		if c.Degree == "reduced" {
			score *= .5
		}
		if groupSigns[groupKey(c)] == 3 && c.Group != "" {
			// Mixed-polarity alternatives share signed ranking utility. Keep
			// their group on one score axis, independent of the winning side.
			if negative {
				score = -score
			}
			negative = false
		}
		prior, exists := groups[key]
		if !exists || !negative && score > prior.score || negative && score < prior.score {
			groups[key] = scoreGroup{scope: scope, kind: kind, negative: negative, score: score}
		}
	}
	type aggregate struct {
		sum   float64
		count int
	}
	facets := map[string]aggregate{}
	meta := map[string]scoreGroup{}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := groups[key]
		facetKey := g.scope + "\x00" + g.kind
		if g.negative {
			facetKey += "\x00negative"
		}
		f := facets[facetKey]
		f.sum += g.score
		f.count++
		facets[facetKey], meta[facetKey] = f, g
	}
	type stageScore struct{ positive, negative aggregate }
	stages := map[string]stageScore{}
	var global stageScore
	keys = keys[:0]
	for key := range facets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		f, g := facets[key], meta[key]
		stage := global
		journey := strings.HasPrefix(g.scope, "journey_")
		if journey {
			stage = stages[g.scope]
		}
		destination := &stage.positive
		if g.negative {
			destination = &stage.negative
		}
		destination.sum += f.sum / float64(f.count)
		destination.count++
		if journey {
			stages[g.scope] = stage
		} else {
			global = stage
		}
	}
	mean := func(a aggregate) float64 {
		if a.count == 0 {
			return 0
		}
		return a.sum / float64(a.count)
	}
	keys = keys[:0]
	for key := range stages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	best, bestNet := stageScore{}, -3.0
	for _, key := range keys {
		s := stages[key]
		if net := mean(s.positive) - mean(s.negative); net > bestNet {
			best, bestNet = s, net
		}
	}
	if best.positive.count > 0 {
		global.positive.sum += mean(best.positive)
		global.positive.count++
	}
	if best.negative.count > 0 {
		global.negative.sum += mean(best.negative)
		global.negative.count++
	}
	if global.positive.count > 0 {
		candidate.Scores.SemanticMatch, candidate.Available.SemanticMatch = mean(global.positive), true
	}
	if global.negative.count > 0 {
		candidate.Scores.SemanticNegativeMatch, candidate.Available.SemanticNegativeMatch = mean(global.negative), true
	}
}
