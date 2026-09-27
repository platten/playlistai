package evaluation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func relevanceFixture(families int) []RelevanceRun {
	zero := 0
	var runs []RelevanceRun
	for index := 0; index < families; index++ {
		for _, variant := range []string{"baseline", "current"} {
			track := BlindTrack{ID: variant, Artist: "fixture artist", Title: "fixture recording"}
			runs = append(runs, RelevanceRun{FamilyID: fmt.Sprintf("family-%d", index), Prompt: fmt.Sprintf("fixture request %d", index), Split: "heldout", Variant: variant, InputMode: "frozen", CacheCondition: "warm", Requested: 2, Tracks: []BlindTrack{track}, Candidates: []BlindTrack{track}, Milliseconds: 100, ConstraintViolations: &zero})
		}
	}
	return runs
}

func TestRelevanceBlindPoolAndUnknownJudgments(t *testing.T) {
	runs := relevanceFixture(1)
	bundle, keys, err := PoolRelevanceRuns(runs, "seed")
	if err != nil {
		t.Fatal(err)
	}
	again, againKeys, err := PoolRelevanceRuns(runs, "seed")
	if err != nil || !reflect.DeepEqual(bundle, again) || !reflect.DeepEqual(keys, againKeys) {
		t.Fatal("blind packet is not deterministic")
	}
	raw, _ := json.Marshal(bundle)
	if strings.Contains(string(raw), `"variant"`) || strings.Contains(string(raw), `"scores"`) || strings.Contains(string(raw), `"split"`) {
		t.Fatal("packet leaks experimental identity")
	}
	report, err := EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotionEligible || report.PairedRequestFit != nil {
		t.Fatalf("unknown judgments claimed improvement: %+v", report)
	}
	for _, metric := range report.Metrics {
		if metric.JudgedPrecision != nil || metric.RequestFit != nil || metric.NDCG != nil || metric.RelevantCandidateCoverage != nil || metric.UsefulPartial != nil {
			t.Fatalf("unknown judgments became a negative: %+v", metric)
		}
	}
	grade := 3.0
	bundle.Cases[0].Grades["current"] = RelevanceGrade{RequestFit: &grade}
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range report.Metrics {
		if metric.Variant == "current" && (metric.Judged != 1 || *metric.JudgedPrecision != 1 || metric.NDCG != nil || metric.PoolJudged != 1 || metric.PoolSize != 2 || metric.UsefulPartial == nil || !*metric.UsefulPartial) {
			t.Fatalf("partial judgments were mishandled: %+v", metric)
		}
	}
}

func TestRelevancePromotionRequiresFullIndependentHeldoutChecks(t *testing.T) {
	runs := relevanceFixture(20)
	bundle, keys, err := PoolRelevanceRuns(runs, "seed")
	if err != nil {
		t.Fatal(err)
	}
	low, high := 1.0, 3.0
	for index := range bundle.Cases {
		bundle.Cases[index].Grades["baseline"] = RelevanceGrade{RequestFit: &low}
		bundle.Cases[index].Grades["current"] = RelevanceGrade{RequestFit: &high}
	}
	report, err := EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.PromotionEligible || report.PairedRequestFit.Cases != 20 || report.PairedRequestFit.Low95 != 2 {
		t.Fatalf("complete synthetic positive comparison: %+v", report)
	}
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", false)
	if err != nil || report.PromotionEligible {
		t.Fatal("unfrozen policy promoted")
	}
	keys.Keys[0].Run.Milliseconds = 300001
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil || report.PromotionEligible {
		t.Fatal("over-budget run promoted")
	}
	keys.Keys[0].Run.GenerationLimitMilliseconds = 600000
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil || !report.PromotionEligible {
		t.Fatal("recorded ten-minute budget was evaluated as a legacy five-minute budget")
	}
	keys.Keys[0].Run.Milliseconds = 600001
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil || report.PromotionEligible {
		t.Fatal("ten-minute budget could be exceeded")
	}
	keys.Keys[0].Run.Milliseconds = 100
	keys.Keys[0].Run.ConstraintViolations = nil
	report, err = EvaluateRelevance(bundle, keys, "baseline", "current", true)
	if err != nil || report.PromotionEligible {
		t.Fatal("unchecked constraints promoted")
	}
}

func TestRelevanceRejectsLeakageInvalidGradesAndPacketTampering(t *testing.T) {
	runs := relevanceFixture(1)
	leak := runs[0]
	leak.Split = "development"
	leak.InputMode = "raw"
	if _, _, err := PoolRelevanceRuns(append(runs, leak), "seed"); err == nil {
		t.Fatal("family split leakage accepted")
	}
	bundle, keys, err := PoolRelevanceRuns(runs, "seed")
	if err != nil {
		t.Fatal(err)
	}
	invalid := 4.0
	bundle.Cases[0].Grades["baseline"] = RelevanceGrade{RequestFit: &invalid}
	if _, err := EvaluateRelevance(bundle, keys, "baseline", "current", true); err == nil {
		t.Fatal("out-of-range grade accepted")
	}
	bundle.Cases[0].Grades["baseline"] = RelevanceGrade{}
	bundle.Cases[0].Prompt = "changed request"
	if _, err := EvaluateRelevance(bundle, keys, "baseline", "current", true); err == nil {
		t.Fatal("changed blind request accepted")
	}
}

func TestRelevanceFamiliesFreezeBalancedDisjointSplitAndCoverage(t *testing.T) {
	raw, err := os.ReadFile("testdata/enhanced-relevance-families-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "d0b19e7cf4585976beac7162b781b9e5f4c0f86f92081c83ff093479ad0a0600" {
		t.Fatal("v1 families and split are frozen; introduce a new version for intentional changes")
	}
	var families []struct {
		FamilyID, Split, Prompt          string
		LeakageGroups, Tags, Paraphrases []string
	}
	if err = json.Unmarshal(raw, &families); err != nil {
		t.Fatal(err)
	}
	if len(families) != 40 {
		t.Fatalf("families=%d", len(families))
	}
	seen, splits, groups, tags := map[string]bool{}, map[string]int{}, map[string]string{}, map[string]bool{}
	for _, family := range families {
		if family.FamilyID == "" || seen[family.FamilyID] || family.Prompt == "" || len(family.Paraphrases) == 0 || len(family.LeakageGroups) == 0 {
			t.Fatalf("invalid family: %+v", family)
		}
		seen[family.FamilyID] = true
		splits[family.Split]++
		for _, group := range family.LeakageGroups {
			if previous, ok := groups[group]; ok && previous != family.Split {
				t.Fatalf("split leakage: %s", group)
			}
			groups[group] = family.Split
		}
		for _, tag := range family.Tags {
			tags[tag] = true
		}
	}
	if splits["development"] != 20 || splits["heldout"] != 20 {
		t.Fatalf("unbalanced split: %v", splits)
	}
	for _, tag := range []string{"descriptive", "track-similarity", "artist-similarity", "multiple-references", "exclusions", "instrumental", "classical", "journey", "library-only"} {
		if !tags[tag] {
			t.Errorf("missing %s coverage", tag)
		}
	}
}
