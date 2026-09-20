package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
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
	snapshot := libraryindex.ProgressSnapshot{Files: 12, Total: 20, Finished: 9, Queued: 7, Leased: 4, Failed: 2, Retries: 3}
	title := progressTitle("Scanning & analyzing", snapshot, progressJobs)
	for _, want := range []string{"Scanning & analyzing", "12 audio files", "7 queued", "4 active", "9/20 finished", "2 failed", "3 retries"} {
		if !strings.Contains(title, want) {
			t.Fatalf("title %q does not contain %q", title, want)
		}
	}
}

func TestScanProgressDoesNotClaimQueuedAnalysisFinished(t *testing.T) {
	snapshot := libraryindex.ProgressSnapshot{Files: 12, Total: 12, Queued: 12}
	title := progressTitle("Complete", snapshot, progressScan)
	if title != "Complete • 12 audio files discovered • 12 queued for processing" {
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

func TestLargeFLACActivityUsesWarningStyle(t *testing.T) {
	for _, test := range []struct {
		file libraryindex.FileActivity
		want bool
	}{
		{file: libraryindex.FileActivity{RelativePath: "large.flac", Size: largeFLACWarningBytes + 1, Extension: ".FLAC"}, want: true},
		{file: libraryindex.FileActivity{RelativePath: "boundary.flac", Size: largeFLACWarningBytes, Extension: ".flac"}, want: false},
		{file: libraryindex.FileActivity{RelativePath: "large.mp3", Size: largeFLACWarningBytes + 1, Extension: ".mp3"}, want: false},
	} {
		if got := progressActivityForFile(test.file).warning; got != test.want {
			t.Errorf("file %+v warning=%v want %v", test.file, got, test.want)
		}
	}
	previous := pterm.PrintColor
	pterm.EnableColor()
	defer func() { pterm.PrintColor = previous }()
	box := progressActivityBox("Currently processing", "large.flac", true)
	if !strings.Contains(box, "\x1b[31m") {
		t.Fatalf("large FLAC box did not use red text: %q", box)
	}
}

func TestProgressBarSurvivesGrowingTotalAndRedrawDeadline(t *testing.T) {
	output := &bytes.Buffer{}
	bar, err := pterm.DefaultProgressbar.WithWriter(output).WithTotal(1).WithCurrent(0).WithShowTitle(false).WithShowElapsedTime(false).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = bar.Stop() }()
	_ = latestProgressLine(output)
	first := updateProgressBar(bar, output, libraryindex.ProgressSnapshot{Total: 1, Finished: 1}, progressJobs)
	if !strings.Contains(first, "100%") {
		t.Fatalf("completed initial bar = %q", first)
	}
	grown := updateProgressBar(bar, output, libraryindex.ProgressSnapshot{Total: 3, Finished: 1}, progressJobs)
	plain := pterm.RemoveColorFromString(grown)
	if grown == "" || !strings.Contains(plain, "33%") || !strings.Contains(plain, "1/3") {
		t.Fatalf("growing total hid or stuck the bar: %q", grown)
	}
	now := time.Now()
	if progressRedrawDue(now.Add(-29*time.Second), now) || !progressRedrawDue(now.Add(-30*time.Second), now) {
		t.Fatal("30-second redraw deadline changed")
	}
}

func TestScanProgressBarUsesDiscoveredAudioFileCount(t *testing.T) {
	output := &bytes.Buffer{}
	bar, err := pterm.DefaultProgressbar.WithWriter(output).WithTotal(1).WithCurrent(0).WithShowTitle(false).WithShowElapsedTime(false).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = bar.Stop() }()
	_ = latestProgressLine(output)
	line := pterm.RemoveColorFromString(updateProgressBar(bar, output, libraryindex.ProgressSnapshot{Files: 23, Total: 17}, progressScan))
	if bar.Total != 23 || bar.Current != 23 || !strings.Contains(line, "23/23") {
		t.Fatalf("scan progress did not use discovered audio file count: total=%d current=%d line=%q", bar.Total, bar.Current, line)
	}
}

func TestActivityRendersWithoutPerFileDurableProgressQuery(t *testing.T) {
	progress := &pipelineProgress{
		phase:   make(chan progressPhase, 1),
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
	progress.SetCurrentFile(libraryindex.FileActivity{RelativePath: "Artist/Track.flac", Size: 123, Extension: ".flac"})
	select {
	case <-reader.calls:
		t.Fatal("file activity triggered a durable progress query")
	case <-time.After(100 * time.Millisecond):
	}
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

type slowProgressReader struct {
	delay time.Duration
	calls chan time.Time
}

func (r slowProgressReader) Progress(ctx context.Context, _ map[string]string) (libraryindex.ProgressSnapshot, error) {
	started := time.Now()
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return libraryindex.ProgressSnapshot{}, ctx.Err()
	}
	r.calls <- started
	return libraryindex.ProgressSnapshot{Total: int64(len(r.calls))}, nil
}

func TestProgressPollerSpacesQueriesByTheirDuration(t *testing.T) {
	const delay = 400 * time.Millisecond
	reader := slowProgressReader{delay: delay, calls: make(chan time.Time, 8)}
	out := make(chan polledProgressSnapshot, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pollProgressSnapshots(ctx, reader, nil, out, func() progressGeneration { return progressGeneration{} })
	first := <-reader.calls
	<-out
	second := <-reader.calls
	if gap, minimum := second.Sub(first), delay+progressQueryBudget*delay; gap < minimum {
		t.Fatalf("progress queries %v apart, want at least %v for a %v query", gap, minimum, delay)
	}
}

func TestAnalysisETAUsesRecentThroughput(t *testing.T) {
	var eta analysisETA
	start := time.Unix(0, 0)
	if suffix := eta.suffix(100); suffix != " • ETA estimating…" {
		t.Fatalf("empty estimator suffix=%q", suffix)
	}
	// 60 files per minute for 20 minutes; only the last 10 minutes count.
	for second := 0; second <= 1200; second += 5 {
		finished := int64(second)
		if second > 600 {
			finished = 600 + int64(second-600)/2 // slows to 30 files/min
		}
		eta.observe(start.Add(time.Duration(second)*time.Second), finished)
	}
	left, perMinute, ok := eta.estimate(900)
	if !ok || perMinute < 29 || perMinute > 31 {
		t.Fatalf("rate=%.2f ok=%v, want the recent 30 files/min", perMinute, ok)
	}
	if left < 29*time.Minute || left > 31*time.Minute {
		t.Fatalf("eta=%v, want about 30m for 900 files", left)
	}
	if got := eta.suffix(900); got != " • ETA 30m (30 files/min)" {
		t.Fatalf("suffix=%q", got)
	}
	if eta.suffix(0) != "" {
		t.Fatal("finished run still shows an ETA")
	}
}

func TestAnalysisETAWaitsForEvidenceAndResetsOnNewCounter(t *testing.T) {
	var eta analysisETA
	start := time.Unix(0, 0)
	// Idle time before the first finished file is not counted.
	eta.observe(start, 0)
	eta.observe(start.Add(5*time.Minute), 0)
	eta.observe(start.Add(5*time.Minute+4*time.Second), 4)
	if _, _, ok := eta.estimate(50); ok {
		t.Fatal("estimated from less than the minimum window")
	}
	eta.observe(start.Add(5*time.Minute+10*time.Second), 10)
	if _, perMinute, ok := eta.estimate(50); !ok || perMinute < 59 || perMinute > 61 {
		t.Fatalf("rate=%.2f ok=%v, want 60 files/min excluding the idle scan", perMinute, ok)
	}
	eta.observe(start.Add(6*time.Minute), 5) // new epoch restarted the counter
	if _, _, ok := eta.estimate(50); ok {
		t.Fatal("estimate mixed samples across a counter reset")
	}
	// A stall inside the window lowers the rate rather than freezing it.
	var stalled analysisETA
	stalled.observe(start, 0)
	stalled.observe(start.Add(time.Minute), 60)
	stalled.observe(start.Add(3*time.Minute), 60)
	if _, perMinute, ok := stalled.estimate(60); !ok || perMinute < 19 || perMinute > 21 {
		t.Fatalf("stalled rate=%.2f ok=%v, want 20 files/min", perMinute, ok)
	}
}

func TestFormatETA(t *testing.T) {
	for input, want := range map[time.Duration]string{
		20 * time.Second:            "<1m",
		42 * time.Minute:            "42m",
		3*time.Hour + 5*time.Minute: "3h05m",
		80 * time.Hour:              "3d08h",
	} {
		if got := formatETA(input); got != want {
			t.Fatalf("formatETA(%v)=%q want %q", input, got, want)
		}
	}
}

type blockingProgressReader struct {
	started  chan struct{}
	canceled chan struct{}
}

func (r blockingProgressReader) Progress(ctx context.Context, _ map[string]string) (libraryindex.ProgressSnapshot, error) {
	close(r.started)
	<-ctx.Done()
	close(r.canceled)
	return libraryindex.ProgressSnapshot{}, ctx.Err()
}

func TestProgressStopCancelsBlockedSnapshotWithoutWaitingForTimeout(t *testing.T) {
	progress := &pipelineProgress{phase: make(chan progressPhase, 1), current: make(chan progressDisplayActivity, 1), stop: make(chan bool, 1), done: make(chan struct{})}
	output := &bytes.Buffer{}
	bar, err := pterm.DefaultProgressbar.WithWriter(output).WithTotal(1).Start("Scanning")
	if err != nil {
		t.Fatal(err)
	}
	display, err := os.CreateTemp(t.TempDir(), "display")
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	area := cursor.NewArea().WithWriter(display)
	reader := blockingProgressReader{started: make(chan struct{}), canceled: make(chan struct{})}
	go progress.run(context.Background(), reader, nil, bar, output, &area, "Scanning", progressScan)
	<-reader.started
	progress.SetPhase("Analyzing")
	progress.SetCurrentOperation("cached render")
	stopped := make(chan struct{})
	go func() { progress.Stop(false); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop blocked on the snapshot query")
	}
	select {
	case <-reader.canceled:
	case <-time.After(time.Second):
		t.Fatal("snapshot query was not canceled")
	}
	if _, err := display.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(display)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "Stopped") {
		t.Fatalf("missing cached final render: %q", contents)
	}
}

type gatedProgressReader struct{ started, release chan struct{} }

func (r gatedProgressReader) Progress(ctx context.Context, _ map[string]string) (libraryindex.ProgressSnapshot, error) {
	close(r.started)
	select {
	case <-r.release:
		return libraryindex.ProgressSnapshot{Files: 99}, nil
	case <-ctx.Done():
		return libraryindex.ProgressSnapshot{}, ctx.Err()
	}
}

func TestProgressPollerDiscardsSnapshotFromPreviousGeneration(t *testing.T) {
	var generation atomic.Uint64
	reader := gatedProgressReader{started: make(chan struct{}), release: make(chan struct{})}
	out := make(chan polledProgressSnapshot, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pollProgressSnapshots(ctx, reader, nil, out, func() progressGeneration { return progressGeneration{scope: generation.Load()} })
	<-reader.started
	generation.Add(1)
	close(reader.release)
	select {
	case got := <-out:
		t.Fatalf("stale counts published: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestProgressScopeGenerationChangesAtEpochAndFreeze(t *testing.T) {
	reader := &scopedProgressReader{}
	progress := &pipelineProgress{}
	first := progress.snapshotGeneration(reader)
	reader.SetScanEpoch(1)
	second := progress.snapshotGeneration(reader)
	reader.FreezeEpoch(1)
	third := progress.snapshotGeneration(reader)
	reader.SetSemanticJobs(map[string]string{"clap": "clap/v1"})
	fourth := progress.snapshotGeneration(reader)
	if first == second || second == third || third == fourth {
		t.Fatal("scope transitions reused a generation")
	}
}

func TestProgressPhasePanelUsesPtermWidget(t *testing.T) {
	panel := pterm.RemoveColorFromString(progressPhasePanel("Extracting CLAP embeddings"))
	if !strings.Contains(panel, "Active phase") || !strings.Contains(panel, "Extracting CLAP embeddings") || !strings.Contains(panel, "─") {
		t.Fatalf("phase panel = %q", panel)
	}
}

func TestSelectSemanticJobsKeepsOnlyCurrentPhase(t *testing.T) {
	all := map[string]string{"metadata": "metadata/v1", "audio": "mert/v1", "clap": "clap/v1"}
	selected := selectSemanticJobs(all, "clap")
	if len(selected) != 1 || selected["clap"] != "clap/v1" {
		t.Fatalf("selected jobs = %#v", selected)
	}
	selected["clap"] = "changed"
	if all["clap"] != "clap/v1" {
		t.Fatal("phase selection aliased the source map")
	}
}
