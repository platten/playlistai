package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	listeningeval "github.com/platten/playlistai/internal/evaluation"
)

func TestListeningCLIProducesBlindPacketAndUnknownReport(t *testing.T) {
	dir := t.TempDir()
	input, packet, key := filepath.Join(dir, "runs.json"), filepath.Join(dir, "packet.json"), filepath.Join(dir, "key.json")
	var runs []listeningeval.RelevanceRun
	for _, variant := range []string{"baseline", "current"} {
		runs = append(runs, listeningeval.RelevanceRun{FamilyID: "family", Prompt: "quiet instrumentals", Split: "development", Variant: variant, InputMode: "frozen", CacheCondition: "unknown", Requested: 2, Tracks: []listeningeval.BlindTrack{{ID: "recording"}}, Milliseconds: 100})
	}
	raw, err := json.Marshal(struct {
		ListeningRuns []listeningeval.RelevanceRun `json:"listeningRuns"`
	}{runs})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = run([]string{"-pool", input, "-blind-output", packet, "-key", key}, &output); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = run([]string{"-judgments", packet, "-key", key}, &output); err != nil {
		t.Fatal(err)
	}
	var report listeningeval.RelevanceReport
	if err = json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.PromotionEligible || len(report.Metrics) != 2 || report.Metrics[0].RequestFit != nil {
		t.Fatalf("CLI invented listening evidence: %+v", report)
	}
	if err = run([]string{"-pool", input, "-blind-output", packet, "-key", packet}, &output); err == nil {
		t.Fatal("identity key may overwrite listener packet")
	}
}
