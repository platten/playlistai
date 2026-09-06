package evaluation

import (
	"fmt"
	"sort"
	"time"
)

// TemporalSplitCases creates global chronological 60/20/20 boundaries. Equal
// timestamps remain in the earlier split so no future case leaks backward.
func TemporalSplitCases(cases []RecommendationCase) (TemporalSplit, error) {
	if len(cases) < 5 {
		return TemporalSplit{}, fmt.Errorf("evaluation: at least 5 timestamped recommendation cases are required for a temporal split")
	}
	ordered := append([]RecommendationCase(nil), cases...)
	seen := map[string]struct{}{}
	for _, item := range ordered {
		if item.ID == "" || item.OccurredAt.IsZero() {
			return TemporalSplit{}, fmt.Errorf("evaluation: recommendation case id and occurredAt are required")
		}
		if _, exists := seen[item.ID]; exists {
			return TemporalSplit{}, fmt.Errorf("evaluation: duplicate recommendation case id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OccurredAt.Equal(ordered[j].OccurredAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].OccurredAt.Before(ordered[j].OccurredAt)
	})
	trainIndex := max(0, min(len(ordered)-2, (len(ordered)*60+99)/100-1))
	devIndex := max(trainIndex+1, min(len(ordered)-1, (len(ordered)*80+99)/100-1))
	result := TemporalSplit{TrainEnd: ordered[trainIndex].OccurredAt, DevelopmentEnd: ordered[devIndex].OccurredAt, Assignments: map[string]Split{}}
	for _, item := range ordered {
		split := SplitTest
		if !item.OccurredAt.After(result.TrainEnd) {
			split = SplitTrain
		} else if !item.OccurredAt.After(result.DevelopmentEnd) {
			split = SplitDevelopment
		}
		result.Assignments[item.ID] = split
	}
	counts := map[Split]int{}
	for _, split := range result.Assignments {
		counts[split]++
	}
	if counts[SplitTrain] == 0 || counts[SplitDevelopment] == 0 || counts[SplitTest] == 0 {
		return TemporalSplit{}, fmt.Errorf("evaluation: timestamps cannot produce non-empty train, development, and test splits")
	}
	return result, nil
}

// ProfileEvents returns only events available before both the query and the
// split's training boundary. Test profiles may use development-era interactions
// after parameters are frozen, but never test-era or future interactions.
func ProfileEvents(records []InteractionRecord, listener string, queryAt time.Time, split Split, boundaries TemporalSplit) []InteractionRecord {
	cutoff := boundaries.TrainEnd
	if split == SplitTest {
		cutoff = boundaries.DevelopmentEnd
	}
	if queryAt.Before(cutoff) {
		cutoff = queryAt
	}
	var result []InteractionRecord
	for _, record := range records {
		if record.ListenerID == listener && record.Event.OccurredAt.Before(queryAt) && !record.Event.OccurredAt.After(cutoff) {
			result = append(result, record)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Event.OccurredAt.Equal(result[j].Event.OccurredAt) {
			return result[i].Event.ID < result[j].Event.ID
		}
		return result[i].Event.OccurredAt.Before(result[j].Event.OccurredAt)
	})
	return result
}
