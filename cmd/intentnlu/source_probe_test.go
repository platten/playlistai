package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestSourceProbeUsesActualPreparserAndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
	raw := []byte(`{"schema":"intent-nlu-review/v1","records":[{"id":"count","prompt":"Give me 12 tracks for a 45 minute run."}]}`)
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := run(context.Background(), []string{"source-probe", "--input", input, "--output", output}, &log, &log); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var results []struct {
		Translation core.IntentTranslation `json:"translation"`
	}
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, atom := range results[0].Translation.Atoms {
		values[atom.Kind] = atom.Value
	}
	if values["count"] != "12" || values["duration"] != "2700" {
		t.Fatalf("lost source facts: %v", values)
	}
	if err := sourceProbe(context.Background(), []string{"--input", input, "--output", input}, &log, &log); err == nil {
		t.Fatal("overwrote source annotations")
	}
	retained, _ := os.ReadFile(input)
	if !bytes.Equal(retained, raw) {
		t.Fatal("source changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sourceProbe(ctx, []string{"--input", input, "--output", filepath.Join(dir, "cancelled.json")}, &log, &log); err == nil {
		t.Fatal("ignored cancellation")
	}
}
