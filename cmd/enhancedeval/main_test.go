package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func fixture() cohort {
	return cohort{Version: 1, Name: "synthetic", Provenance: "synthetic vectors; no musical quality evidence", Spaces: map[string]string{"catalogAudio": "fixture/a", "catalogCooccurrence": "fixture/c", "clap": "fixture/clap", "mert": "fixture/mert"}, Tracks: []cohortTrack{
		{ID: "q", RecordingID: "q", CatalogAudio: []float64{1, 0}, CatalogCooccurrence: []float64{1, 0}, CLAP: []float64{1, 0}, MERT: []float64{1, 0}},
		{ID: "a", RecordingID: "a", CatalogAudio: []float64{1, 0}, CatalogCooccurrence: []float64{1, 0}, CLAP: []float64{0, 1}, MERT: []float64{0, 1}},
		{ID: "b", RecordingID: "b", CatalogAudio: []float64{0, 1}, CatalogCooccurrence: []float64{0, 1}, CLAP: []float64{1, 0}, MERT: []float64{1, 0}},
	}, HeldOutAdjacency: []adjacency{{"q", "b"}}}
}
func evalFixture(t *testing.T, c cohort) evaluation {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := evaluate(raw, 5)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestMetricsUseFullCommonPoolAndDeterministicOrdering(t *testing.T) {
	c := fixture()
	report := evalFixture(t, c)
	if report.Metrics["catalog"].MeanReciprocalRank != .5 || report.Metrics["mert"].MeanReciprocalRank != 1 || report.Metrics["hybrid"].MeanReciprocalRank != .5 {
		t.Fatalf("metrics %+v", report.Metrics)
	}
	if !reflect.DeepEqual(report, evalFixture(t, c)) {
		t.Fatal("report changed")
	}
	if len(report.PolicySHA256) != 64 || len(report.InputSHA256) != 64 {
		t.Fatal("missing identities")
	}
	c.Tracks[1].CLAP = nil
	r := evalFixture(t, c)
	if r.ComparableTracks != 2 || !reflect.DeepEqual(r.ExcludedTracks, []string{"a"}) || r.Metrics["catalog"].MeanReciprocalRank != 1 {
		t.Fatalf("unmatched candidate pool %+v", r)
	}
	c.HeldOutAdjacency = []adjacency{{"q", "a"}}
	r = evalFixture(t, c)
	if r.SkippedPairs != 1 || len(r.Metrics) != 0 {
		t.Fatal("missing evidence assigned rank")
	}
}
func TestUnlabelledCohortDoesNotInventMetrics(t *testing.T) {
	c := fixture()
	c.HeldOutAdjacency = nil
	r := evalFixture(t, c)
	if len(r.Metrics) != 0 || r.EvaluatedPairs != 0 || len(r.Queries) != 3 {
		t.Fatal("invented quality metrics")
	}
}
func TestMalformedInputsRejected(t *testing.T) {
	for name, mutate := range map[string]func(*cohort){
		"zero":                func(c *cohort) { c.Tracks[0].MERT = []float64{0, 0} },
		"dimensions":          func(c *cohort) { c.Tracks[0].MERT = []float64{1} },
		"space":               func(c *cohort) { delete(c.Spaces, "mert") },
		"duplicate recording": func(c *cohort) { c.Tracks[0].RecordingID = "a" },
		"unknown pair":        func(c *cohort) { c.HeldOutAdjacency = []adjacency{{"q", "missing"}} },
		"duplicate pair":      func(c *cohort) { c.HeldOutAdjacency = append(c.HeldOutAdjacency, c.HeldOutAdjacency[0]) },
		"large":               func(c *cohort) { c.Tracks = make([]cohortTrack, 257) },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture()
			mutate(&c)
			raw, _ := json.Marshal(c)
			if _, err := evaluate(raw, 5); err == nil {
				t.Fatal("accepted malformed cohort")
			}
		})
	}
	for _, raw := range []string{`{}`, `null`, `{} {}`, `{"version":1,"audioPath":"private.wav"}`} {
		if _, err := evaluate([]byte(raw), 5); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
