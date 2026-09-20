package evaluation

import (
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// StartingPointJudgments are independent listening/eligibility labels, never
// inferred from tag agreement or a generator's own score. Omitted IDs are unknown.
type StartingPointJudgments struct {
	SeedRelevance     map[string]float64 `json:"seedRelevance,omitempty"`     // 0..3
	OpeningRelevance  map[string]float64 `json:"openingRelevance,omitempty"`  // 0..3
	EligibleNeighbors map[string]int     `json:"eligibleNeighbors,omitempty"` // assessed under this request
}

type StartingPointMetrics struct {
	SeedIDs             []string `json:"seedIds"`
	OpeningID           string   `json:"openingId,omitempty"`
	SeedTop1            *float64 `json:"seedTop1,omitempty"`
	SeedTop3            *float64 `json:"seedTop3,omitempty"`
	SeedJudgedAt3       int      `json:"seedJudgedAt3"`
	SeedReturnedAt3     int      `json:"seedReturnedAt3"`
	OpeningRelevance    *float64 `json:"openingRelevance,omitempty"`
	JudgedNeighborhoods int      `json:"judgedNeighborhoods"`
	DeadEndRate         *float64 `json:"deadEndRate,omitempty"`
}

// SourceQuality describes output coverage, not a desired source quota. Equal
// mean grades cannot establish neutrality without controlled paired judgments.
type SourceQuality struct {
	Returned      int      `json:"returned"`
	Judged        int      `json:"judged"`
	MeanRelevance *float64 `json:"meanRelevance,omitempty"`
}

func validGrade(grade float64) bool {
	return !math.IsNaN(grade) && !math.IsInf(grade, 0) && grade >= 0 && grade <= 3
}

// StartingPoints evaluates the resolved seed enumeration separately from the
// actual first output track. Top3 is the mean grade / 3 of up to three returned
// seeds; it is unavailable if any returned seed in that prefix is unjudged.
func StartingPoints(seedIDs, outputIDs []string, judgments *StartingPointJudgments) StartingPointMetrics {
	m := StartingPointMetrics{SeedIDs: []string{}}
	seen := map[string]bool{}
	for _, id := range seedIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			m.SeedIDs = append(m.SeedIDs, id)
		}
	}
	if len(outputIDs) > 0 {
		m.OpeningID = outputIDs[0]
	}
	m.SeedReturnedAt3 = min(3, len(m.SeedIDs))
	if judgments == nil {
		return m
	}
	var sum float64
	for i, id := range m.SeedIDs[:m.SeedReturnedAt3] {
		if grade, ok := judgments.SeedRelevance[id]; ok && validGrade(grade) {
			m.SeedJudgedAt3++
			sum += grade / 3
			if i == 0 {
				v := grade / 3
				m.SeedTop1 = &v
			}
		}
	}
	if m.SeedReturnedAt3 > 0 && m.SeedReturnedAt3 == m.SeedJudgedAt3 {
		v := sum / float64(m.SeedReturnedAt3)
		m.SeedTop3 = &v
	}
	if grade, ok := judgments.OpeningRelevance[m.OpeningID]; ok && m.OpeningID != "" && validGrade(grade) {
		v := grade / 3
		m.OpeningRelevance = &v
	}
	dead := 0
	for _, id := range m.SeedIDs {
		if count, ok := judgments.EligibleNeighbors[id]; ok && count >= 0 {
			m.JudgedNeighborhoods++
			if count == 0 {
				dead++
			}
		}
	}
	if m.JudgedNeighborhoods > 0 {
		v := float64(dead) / float64(m.JudgedNeighborhoods)
		m.DeadEndRate = &v
	}
	return m
}

func resolvedSeedIDs(intent core.MusicIntent) []string {
	var ids []string
	for _, ref := range core.RetrievalReferences(intent) {
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil && len(ref.Resolution.Selected.Representatives) > 0 {
			for _, representative := range ref.Resolution.Selected.Representatives {
				ids = append(ids, representative.TrackID)
			}
		} else if ref.TrackID != "" {
			ids = append(ids, ref.TrackID)
		}
	}
	return ids
}

func SourceQualityAtK(ids []string, relevance map[string]float64, k int) map[string]SourceQuality {
	result := map[string]SourceQuality{}
	if k <= 0 {
		return result
	}
	sums := map[string]float64{}
	for _, id := range ids[:min(k, len(ids))] {
		source := "outside"
		if strings.HasPrefix(id, "local:") {
			source = "personal_pack"
		} else if strings.HasPrefix(id, "pack:") {
			source = "shared_pack"
		}
		m := result[source]
		m.Returned++
		if grade, ok := relevance[id]; ok && validGrade(grade) {
			m.Judged++
			sums[source] += grade / 3
		}
		result[source] = m
	}
	for source, m := range result {
		if m.Judged > 0 {
			v := sums[source] / float64(m.Judged)
			m.MeanRelevance = &v
		}
		result[source] = m
	}
	return result
}
