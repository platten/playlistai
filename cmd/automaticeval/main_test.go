package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/evaluation"
)

func TestAutomaticCLIValidationAndExclusivePrivateOutput(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"corpus"}, {"evaluate"}, {"evaluate", "extra"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeResult(path, nil, map[string]string{"state": "insufficient"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := writeResult(path, nil, "replacement"); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("collision changed artifact")
	}
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private output permissions: %v %v", info, err)
	}
	var result struct {
		State string `json:"state"`
	}
	if err := readJSON(path, &result); err != nil || result.State != "insufficient" {
		t.Fatal("valid report did not decode")
	}
	if err := os.WriteFile(path, append(before, []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(path, &result); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestAutomaticCLIInsufficientRetainsReportAndExitsFailure(t *testing.T) {
	dir := t.TempDir()
	hash := func(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
	c := evaluation.AutomaticCorpus{Version: evaluation.AutomaticCorpusVersion, Seed: "fixture", AnnotationsSHA256: hash("labels"), AudioHashesSHA256: hash("full"), LowAudioHashesSHA256: hash("low")}
	for i := range 600 {
		id, split, label := i+100000, "development", "instrumental"
		if i >= 300 {
			split = "heldout"
		}
		if i%300 >= 150 {
			label = "voice"
		}
		c.Tracks = append(c.Tracks, evaluation.AutomaticCorpusTrack{ID: fmt.Sprintf("track_%d", id), ArtistID: fmt.Sprintf("artist_%d", id), AudioPath: fmt.Sprintf("%02d/%d.mp3", id%100, id), AudioSHA256: hash(fmt.Sprintf("full:%d", id)), LowAudioSHA256: hash(fmt.Sprintf("low:%d", id)), Duration: 100, Split: split, Labels: map[string]string{"voice_instrumental": label}})
	}
	m := evaluation.AutomaticMeasurements{Version: evaluation.AutomaticEvaluationVersion, CorpusSHA256: evaluation.AutomaticCorpusSHA256(c), ProducerSourceSHA256: hash("source"), PolicySHA256: hash("policy"), FeatureSnapshotSHA256: hash("features")}
	manifest, input, output := filepath.Join(dir, "corpus.json"), filepath.Join(dir, "input.json"), filepath.Join(dir, "report.json")
	for path, value := range map[string]any{manifest: c, input: m} {
		if err := writeResult(path, nil, value); err != nil {
			t.Fatal(err)
		}
	}
	err := run([]string{"evaluate", "-manifest", manifest, "-measurements", input, "-output", output}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("missing measurements passed: %v", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report evaluation.AutomaticReport
	if err := json.Unmarshal(raw, &report); err != nil || report.State != "insufficient" || len(report.Results) != 6 {
		t.Fatalf("incomplete report lost: %+v %v", report, err)
	}
}
