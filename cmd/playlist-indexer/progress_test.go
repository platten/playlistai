package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestProgressTitleReportsDurableState(t *testing.T) {
	snapshot := libraryindex.ProgressSnapshot{Files: 12, Total: 20, Finished: 9, Queued: 7, Leased: 4, Failed: 2}
	title := progressTitle("Scanning & analyzing", snapshot, progressJobs)
	for _, want := range []string{"Scanning & analyzing", "12 files", "7 queued", "4 active", "9/20 finished", "2 failed"} {
		if !strings.Contains(title, want) {
			t.Fatalf("title %q does not contain %q", title, want)
		}
	}
}

func TestScanProgressDoesNotClaimQueuedAnalysisFinished(t *testing.T) {
	snapshot := libraryindex.ProgressSnapshot{Files: 12, Total: 12, Queued: 12}
	title := progressTitle("Complete", snapshot, progressScan)
	if title != "Complete • 12 files discovered • 12 jobs queued for analysis" {
		t.Fatalf("scan completion title = %q", title)
	}
}

func TestActivityProgressReportsLibrarySize(t *testing.T) {
	title := progressTitle("Exporting library pack", libraryindex.ProgressSnapshot{Files: 42}, progressActivity)
	if title != "Exporting library pack • 42 library files" {
		t.Fatalf("activity title = %q", title)
	}
}

func TestProgressIsSilentForNonTerminalWriter(t *testing.T) {
	state, err := libraryindex.OpenState(context.Background(), t.TempDir(), "progress-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var output bytes.Buffer
	progress := startPipelineProgress(context.Background(), state, map[string]string{"metadata": "v1"}, &output, false, "Scanning", progressScan)
	progress.SetPhase("Analyzing")
	progress.Stop(true)
	if output.Len() != 0 {
		t.Fatalf("non-terminal progress wrote %q", output.String())
	}
}

func TestProgressFileNameEscapesControlCharactersAndBoundsWidth(t *testing.T) {
	name := "artist/line\nbreak-" + strings.Repeat("long", 40) + ".flac"
	got := progressFileName(name)
	if strings.ContainsRune(got, '\n') || !strings.Contains(got, `\n`) {
		t.Fatalf("unsafe filename display %q", got)
	}
	if count := len([]rune(got)); count > 96 {
		t.Fatalf("filename display has %d runes: %q", count, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("long filename was not truncated: %q", got)
	}
}

func TestLatestProgressLineUsesMostRecentRender(t *testing.T) {
	output := bytes.NewBufferString("\rfirst\rsecond\n")
	if got := latestProgressLine(output); got != "second" {
		t.Fatalf("latest line = %q", got)
	}
	if output.Len() != 0 {
		t.Fatalf("progress output was not drained: %q", output.String())
	}
}
