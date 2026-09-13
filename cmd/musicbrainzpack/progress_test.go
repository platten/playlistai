package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatProgressShowsBarThroughputETAAndRows(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 10, 0, time.UTC)
	line := formatProgress(progressState{
		label: "Process recordings", done: 50 << 20, total: 100 << 20, rows: 1234567,
		unit: "bytes", started: now.Add(-10 * time.Second), initialDone: 0, initialized: true,
	}, now)
	for _, want := range []string{"Process recordings", "█", "░", "50%", "5.0 MiB/s", "ETA 00:10", "1,234,567 rows"} {
		if !strings.Contains(line, want) {
			t.Fatalf("progress %q does not contain %q", line, want)
		}
	}
}

func TestFormatProgressShowsCompletion(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 5, 0, time.UTC)
	line := formatProgress(progressState{label: "Download artists", unit: "bytes", done: 1024, total: 1024, started: now.Add(-5 * time.Second), finished: now, initialized: true}, now)
	if !strings.Contains(line, "100%") || !strings.Contains(line, "done in 00:05") {
		t.Fatal(line)
	}
}
