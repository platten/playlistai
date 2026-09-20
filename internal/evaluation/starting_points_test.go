package evaluation

import (
	"math"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestStartingPointsKeepUnknownSeparateFromNegative(t *testing.T) {
	j := &StartingPointJudgments{SeedRelevance: map[string]float64{"a": 3, "b": 0}, OpeningRelevance: map[string]float64{"opening": 2}, EligibleNeighbors: map[string]int{"a": 0, "b": 4}}
	m := StartingPoints([]string{"a", "a", "b", "unjudged"}, []string{"opening"}, j)
	if m.SeedTop1 == nil || *m.SeedTop1 != 1 || m.SeedTop3 != nil || m.SeedJudgedAt3 != 2 || m.SeedReturnedAt3 != 3 {
		t.Fatalf("unknown became negative or duplicate counted: %+v", m)
	}
	if m.DeadEndRate == nil || *m.DeadEndRate != .5 || m.JudgedNeighborhoods != 2 {
		t.Fatalf("unknown neighborhood counted: %+v", m)
	}
	if m.OpeningRelevance == nil || *m.OpeningRelevance != 2.0/3 || m.OpeningID == m.SeedIDs[0] {
		t.Fatalf("opening conflated with retrieval seed: %+v", m)
	}
	m = StartingPoints([]string{"a", "b"}, nil, j)
	if m.SeedTop3 == nil || *m.SeedTop3 != .5 || m.OpeningRelevance != nil {
		t.Fatal(m)
	}
	m = StartingPoints(nil, nil, j)
	if m.SeedTop1 != nil || m.SeedTop3 != nil || m.DeadEndRate != nil {
		t.Fatal("empty results graded")
	}
}

func TestSourceQualityReportsJudgedCoverageWithoutSourceQuota(t *testing.T) {
	m := SourceQualityAtK([]string{"local:one:a", "outside-a", "pack:one:b", "outside-unjudged", "local:one:unjudged"}, map[string]float64{"local:one:a": 3, "outside-a": 3, "pack:one:b": 0}, 5)
	if m["personal_pack"].Returned != 2 || m["personal_pack"].Judged != 1 || m["outside"].Judged != 1 || *m["personal_pack"].MeanRelevance != *m["outside"].MeanRelevance || *m["shared_pack"].MeanRelevance != 0 {
		t.Fatalf("source or unknown bias: %+v", m)
	}
	if got := SourceQualityAtK([]string{"unjudged"}, nil, 1)["outside"]; got.MeanRelevance != nil || got.Judged != 0 {
		t.Fatal(got)
	}
}

func TestStartingPointUncertaintyExcludesUnknown(t *testing.T) {
	one := 1.0
	got := uncertainty([]CaseMetrics{{StartingPoints: &StartingPointMetrics{SeedTop1: &one}}, {StartingPoints: &StartingPointMetrics{}}, {Error: "failed", StartingPoints: &StartingPointMetrics{SeedTop1: &one}}})
	if got["seedTop1"].Cases != 1 || got["seedTop1"].Mean != 1 {
		t.Fatal(got["seedTop1"])
	}
	if validGrade(math.NaN()) || validGrade(math.Inf(1)) || validGrade(-1) || validGrade(4) {
		t.Fatal("invalid grade accepted")
	}
}

func TestPriorityPaipackFixtureIsSyntheticAndContainsAllPrompts(t *testing.T) {
	dataset, err := LoadDataset("testdata/paipack-priority-synthetic-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Evidence != EvidenceSynthetic {
		t.Fatal("fixture claims listening evidence")
	}
	if _, err := TemporalSplitCases(dataset.RecommendationCases); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"ambient with lots of piano": false, "radiohead going to marilyn manson": false, "classical music with chello": false, "lively dance music from the 1990s": false}
	for _, item := range dataset.RecommendationCases {
		if _, ok := want[item.Prompt]; ok {
			want[item.Prompt] = true
		}
	}
	for prompt, found := range want {
		if !found {
			t.Fatalf("missing %s", prompt)
		}
	}
}

func TestResolvedSeedsIncludeInferredAndStagesButExcludeNegative(t *testing.T) {
	ref := func(id string) core.IntentReference {
		return core.IntentReference{TrackID: id, Influence: core.InfluencePositive}
	}
	negative := ref("negative")
	negative.Influence = core.InfluenceNegative
	start, end := ref("start"), ref("end")
	intent := core.MusicIntent{References: []core.IntentReference{negative, ref("explicit")}, InferredAnchors: []core.InferredAnchor{{Reference: ref("inferred")}, {Reference: ref("rejected"), Suitability: core.AnchorSuitability{State: core.EvidenceMismatch}}}, Start: &start, Destination: &end}
	ids := resolvedSeedIDs(intent)
	want := []string{"explicit", "inferred", "start", "end"}
	if len(ids) != len(want) {
		t.Fatal(ids)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Fatal(ids)
		}
	}
}
