package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/musicgraph"
)

func TestPrepareInspectAndNoImplicitOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "prepared.json")
	s := musicgraph.Snapshot{Version: musicgraph.Version, PreparedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, b, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err = run(context.Background(), []string{"prepare", "-input", input}, &stdout, &stderr); err == nil {
		t.Fatal("implicit output accepted")
	}
	if err = run(context.Background(), []string{"prepare", "-input", input, "-output", output}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		SHA256 string `json:"sha256"`
	}
	if err = json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err = run(context.Background(), []string{"inspect", "-input", output, "-sha256", receipt.SHA256}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err = run(context.Background(), []string{"prepare", "-input", input, "-output", output}, &stdout, &stderr); err == nil {
		t.Fatal("existing output replaced")
	}
	if err = run(context.Background(), []string{"inspect", "-input", output}, &stdout, &stderr); err == nil {
		t.Fatal("unpinned inspection accepted")
	}
}

func TestCanceledPrepareDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "prepared.json")
	if err := os.WriteFile(input, []byte(`{"version":"prepared-music-graph/v1","preparedAt":"2026-09-01T00:00:00Z","artists":[],"recordings":[],"topRecordings":[],"neighbors":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"prepare", "-input", input, "-output", output}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("canceled output published")
	}
}
