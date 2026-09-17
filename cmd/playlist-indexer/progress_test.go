package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"atomicgo.dev/cursor"
	"github.com/pterm/pterm"

	"github.com/platten/playlistai/internal/libraryindex"
)

type failingProgressReader struct {
	calls chan struct{}
}

func (r failingProgressReader) Progress(context.Context, map[string]string) (libraryindex.ProgressSnapshot, error) {
	if r.calls != nil {
		r.calls <- struct{}{}
	}
	return libraryindex.ProgressSnapshot{}, errors.New("busy")
}

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

func TestProgressActivityDisplayExplainsEnumerationBeforeFirstFile(t *testing.T) {
	title, detail := progressActivityDisplay(progressDisplayActivity{}, "Scanning & analyzing", libraryindex.ProgressSnapshot{})
	if title != "Current activity" || detail != "Scanning directory inventory…" {
		t.Fatalf("unexpected initial activity: title=%q detail=%q", title, detail)
	}
}

func TestProgressActivityDisplayUsesLatestDirectoryAndFile(t *testing.T) {
	tests := []struct {
		activity  progressDisplayActivity
		wantTitle string
		wantPath  string
	}{
		{activity: progressDisplayActivity{title: "Scanning directory", path: "archive/Artist"}, wantTitle: "Scanning directory", wantPath: "archive/Artist"},
		{activity: progressDisplayActivity{title: "Currently processing", path: "Artist/Track.flac"}, wantTitle: "Currently processing", wantPath: "Artist/Track.flac"},
	}
	for _, test := range tests {
		title, detail := progressActivityDisplay(test.activity, "Scanning", libraryindex.ProgressSnapshot{})
		if title != test.wantTitle || detail != test.wantPath {
			t.Errorf("activity %+v rendered title=%q detail=%q", test.activity, title, detail)
		}
	}
}

func TestProgressActivityDisplayShowsHeartbeatForLongWork(t *testing.T) {
	started := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	activity := progressDisplayActivity{title: "Currently processing", path: "Artist/Track.flac", started: started}
	title, detail := progressActivityDisplayAt(activity, "Analyzing", libraryindex.ProgressSnapshot{Leased: 1}, started.Add(3*time.Second))
	if title != "Currently processing" || detail != "Artist/Track.flac\nActive for 3s" {
		t.Fatalf("heartbeat activity: title=%q detail=%q", title, detail)
	}
}

func TestActivityRendersWhenDurableProgressQueryIsBusy(t *testing.T) {
	progress := &pipelineProgress{
		phase:   make(chan string, 1),
		current: make(chan progressDisplayActivity, 1),
		stop:    make(chan bool, 1),
		done:    make(chan struct{}),
	}
	barOutput := &bytes.Buffer{}
	bar, err := pterm.DefaultProgressbar.WithWriter(barOutput).WithTotal(1).Start("Scanning")
	if err != nil {
		t.Fatal(err)
	}
	display, err := os.CreateTemp(t.TempDir(), "progress-display-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	area := cursor.NewArea().WithWriter(display)
	reader := failingProgressReader{calls: make(chan struct{}, 2)}
	go progress.run(context.Background(), reader, nil, bar, barOutput, &area, "Scanning", progressScan)
	<-reader.calls // initial render
	progress.SetCurrentFile("Artist/Track.flac")
	<-reader.calls // activity render
	progress.Stop(false)
	if _, err := display.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(display)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "Artist/Track.flac") {
		t.Fatalf("activity was hidden by progress query failure: %q", contents)
	}
}
