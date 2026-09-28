package multichannel

import (
	"fmt"
	"sort"

	"github.com/platten/playlistai/internal/core"
)

// Rank only within an identical model and clause. Raw CLAP cosines and
// classifier scores never share a numerical scale or become calibrated fit.
func automaticRankEstimates(fits map[string]core.AutomaticFitAssessment) {
	type item struct {
		id     string
		clause int
		score  float64
	}
	channels := map[string][]item{}
	for id, fit := range fits {
		for i, clause := range fit.Clauses {
			for _, signal := range clause.Signals {
				if !signal.ScoreAvailable || !finite(signal.Score) || signal.ModelFingerprint == "" {
					continue
				}
				c := clause.Clause
				key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t", signal.Family, signal.ModelFingerprint, c.Kind, c.Text, c.Scope, c.Degree, c.Negative)
				score := signal.Score
				if c.Negative {
					score = -score
				}
				channels[key] = append(channels[key], item{id, i, score})
			}
		}
	}
	// Stable iteration also keeps floating-point addition deterministic.
	keys := make([]string, 0, len(channels))
	for key := range channels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	counts := map[string]map[int]int{}
	for _, key := range keys {
		items := channels[key]
		sort.Slice(items, func(i, j int) bool {
			if items[i].score != items[j].score {
				return items[i].score > items[j].score
			}
			return items[i].id < items[j].id
		})
		rank := 1
		for i, item := range items {
			if i > 0 && item.score != items[i-1].score {
				rank = i + 1
			}
			fit := fits[item.id]
			clause := &fit.Clauses[item.clause]
			clause.EstimateScore += 61 / (60 + float64(rank))
			clause.EstimateAvailable = true
			fits[item.id] = fit
			if counts[item.id] == nil {
				counts[item.id] = map[int]int{}
			}
			counts[item.id][item.clause]++
		}
	}
	for id, clauses := range counts {
		fit := fits[id]
		for i, count := range clauses {
			fit.Clauses[i].EstimateScore /= float64(count)
		}
		fits[id] = fit
	}
}

// Soft stages need affirmative ranking information and prefer their own best
// stage. Relaxing admission must not make every recording fit every stage.
func automaticStageFits(fit core.AutomaticFitAssessment, scope string, scopes []string) bool {
	if !automaticScopeFits(fit, scope) {
		return false
	}
	soft := false
	for _, c := range fit.Clauses {
		soft = soft || c.Clause.Scope == scope && core.AutomaticEstimatedClause(c.Clause)
	}
	if !soft {
		return true
	}
	score, known := automaticScopeScore(fit, scope)
	if !known || score <= 0 {
		return false
	}
	for _, other := range scopes {
		if v, ok := automaticScopeScore(fit, other); ok && v > score {
			return false
		}
	}
	return true
}

// Combine reference channels by rank, never by maximum cosine across spaces.
func automaticReferenceRanks(candidates []core.Candidate, batch *automaticBatch) map[string]float64 {
	scores := map[string]float64{}
	for channel := 0; channel < 3; channel++ {
		type item struct {
			id    string
			score float64
		}
		spaces := map[string][]item{}
		for _, c := range candidates {
			values := []float64{c.Scores.LibraryMERT, c.Scores.AudioSeedAffinity, c.Scores.CooccurrenceAffinity}
			available := []bool{c.Available.LibraryMERT, c.Available.AudioSeedAffinity, c.Available.CooccurrenceAffinity}
			if available[channel] && finite(values[channel]) {
				space := "catalog"
				if channel == 0 {
					space = batch.mert[c.Track.ID].Source.SpaceID
				}
				spaces[space] = append(spaces[space], item{c.Track.ID, values[channel]})
			}
		}
		for _, items := range spaces {
			sort.Slice(items, func(i, j int) bool {
				if items[i].score != items[j].score {
					return items[i].score > items[j].score
				}
				return items[i].id < items[j].id
			})
			rank := 1
			for i, v := range items {
				if i > 0 && v.score != items[i-1].score {
					rank = i + 1
				}
				scores[v.id] += 61 / (60 + float64(rank)) / 3
			}
		}
	}
	return scores
}
