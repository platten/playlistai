package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/evaluation"
)

func TestAudioEvaluationReportsSyntheticEvidence(t *testing.T) {
	first := int64(10)
	yes := true
	data := evaluation.AudioReviewDataset{Version: 1, ID: "synthetic-control", Evidence: evaluation.EvidenceSynthetic, PolicyVersion: "fixture", DevelopmentSet: "synthetic-only"}
	for _, split := range []string{"development", "heldout"} {
		for _, variant := range []string{"existing", "seed_verified", "candidate_verified"} {
			data.Runs = append(data.Runs, evaluation.AudioReviewRun{CaseID: split, Prompt: "fixture", Split: split, Variant: variant, Requested: 1, Outcome: core.OutcomePartial, Tracks: []evaluation.AudioTrackReview{{ReviewedRecording: evaluation.ReviewedRecording{RecordingID: split + "-recording", ArtistIDs: []string{split + "-artist"}}, MusicalFit: &yes}}, FirstResultMilliseconds: &first, TotalMilliseconds: 20, PeakMemoryBytes: 1024})
		}
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "output.json")
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(input, output); err != nil {
		t.Fatal(err)
	}
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"audioeval", "-input", input, "-output", output}
	main()
	raw, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report evaluation.AudioReviewReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.HumanReviewed || len(report.Metrics) != 6 {
		t.Fatalf("synthetic results misrepresented: %+v", report)
	}
	if err := run(input, dir); err == nil {
		t.Fatal("accepted directory output")
	}
	for _, raw := range []string{"{", `{}`} {
		if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if run(input, output) == nil {
			t.Fatal("accepted invalid dataset")
		}
	}
	if run(filepath.Join(dir, "missing"), output) == nil {
		t.Fatal("accepted missing input")
	}
}
