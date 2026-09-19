package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"atomicgo.dev/cursor"
	"github.com/pterm/pterm"
	"golang.org/x/term"

	"github.com/platten/playlistai/internal/libraryindex"
)

const (
	progressRefreshInterval = time.Second
	// A manifest-scoped aggregate over hundreds of thousands of jobs takes
	// about a second. Background polls wait this multiple of the last query's
	// duration, capping the progress display's share of a read connection.
	progressQueryBudget   = 4
	progressQueryTimeout  = 30 * time.Second
	progressFullRedraw    = 30 * time.Second
	largeFLACWarningBytes = int64(500_000_000)
)

type progressMode uint8

const (
	progressJobs progressMode = iota
	progressScan
	progressActivity
)

type pipelineProgress struct {
	phase      chan string
	current    chan progressDisplayActivity
	stop       chan bool
	done       chan struct{}
	once       sync.Once
	generation atomic.Uint64
}

type progressDisplayActivity struct {
	title   string
	path    string
	started time.Time
	warning bool
}

type progressSnapshotReader interface {
	Progress(context.Context, map[string]string) (libraryindex.ProgressSnapshot, error)
}

type scopedProgressReader struct {
	mu         sync.RWMutex
	state      *libraryindex.State
	epoch      int64
	frozen     bool
	generation uint64
}

func (r *scopedProgressReader) SetScanEpoch(epoch int64) {
	r.mu.Lock()
	r.generation++
	r.epoch = epoch
	r.frozen = false
	r.mu.Unlock()
}

func (r *scopedProgressReader) FreezeEpoch(epoch int64) {
	r.mu.Lock()
	r.generation++
	r.epoch = epoch
	r.frozen = true
	r.mu.Unlock()
}

func (r *scopedProgressReader) ProgressGeneration() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

type progressGeneration struct{ phase, scope uint64 }
type polledProgressSnapshot struct {
	snapshot   libraryindex.ProgressSnapshot
	generation progressGeneration
}

func (p *pipelineProgress) snapshotGeneration(state progressSnapshotReader) progressGeneration {
	generation := progressGeneration{phase: p.generation.Load()}
	if scoped, ok := state.(interface{ ProgressGeneration() uint64 }); ok {
		generation.scope = scoped.ProgressGeneration()
	}
	return generation
}

func (r *scopedProgressReader) Progress(ctx context.Context, semanticJobs map[string]string) (libraryindex.ProgressSnapshot, error) {
	r.mu.RLock()
	epoch := r.epoch
	frozen := r.frozen
	r.mu.RUnlock()
	if epoch > 0 {
		if !frozen {
			return r.state.ScanCandidateProgress(ctx, epoch, semanticJobs)
		}
		return r.state.ScanDiffProgress(ctx, epoch)
	}
	return r.state.Progress(ctx, semanticJobs)
}

func startPipelineProgress(ctx context.Context, state progressSnapshotReader, semanticJobs map[string]string, writer io.Writer, disabled bool, initialPhase string, mode progressMode) *pipelineProgress {
	progress := &pipelineProgress{phase: make(chan string, 1), current: make(chan progressDisplayActivity, 1), stop: make(chan bool, 1), done: make(chan struct{})}
	file, terminal := writer.(*os.File)
	if disabled || !terminal || !term.IsTerminal(int(file.Fd())) || os.Getenv("TERM") == "dumb" {
		close(progress.done)
		return progress
	}

	// PTerm's progress printer writes its content to the configured writer, but
	// its cursor package has a separate global target. Point both at stderr so
	// JSON and other machine-readable stdout output remain untouched.
	cursor.SetTarget(file)
	barOutput := &bytes.Buffer{}
	bar, err := pterm.DefaultProgressbar.
		WithWriter(barOutput).
		WithTotal(1).
		WithCurrent(0).
		WithMaxWidth(110).
		WithShowTitle(false).
		WithShowElapsedTime(false).
		WithBarFiller("░").
		WithRemoveWhenDone(false).
		Start(initialPhase)
	if err != nil {
		cursor.SetTarget(os.Stdout)
		close(progress.done)
		return progress
	}
	area := cursor.NewArea().WithWriter(file)
	go progress.run(ctx, state, semanticJobs, bar, barOutput, &area, initialPhase, mode)
	return progress
}

// Active reports whether the terminal display is rendering. When it is not,
// callers print notable state changes to stderr instead.
func (p *pipelineProgress) Active() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *pipelineProgress) SetPhase(phase string) {
	if p == nil || phase == "" {
		return
	}
	p.generation.Add(1)
	select {
	case p.phase <- phase:
	default:
		select {
		case <-p.phase:
		default:
		}
		select {
		case p.phase <- phase:
		default:
		}
	}
}

// SetCurrentFile updates the live box without blocking a scan or analysis
// worker. With concurrent workers the box intentionally shows the most recent
// file to begin processing rather than implying that only one file is active.
func (p *pipelineProgress) SetCurrentFile(file libraryindex.FileActivity) {
	p.setActivity(progressActivityForFile(file))
}

func progressActivityForFile(file libraryindex.FileActivity) progressDisplayActivity {
	warning := strings.EqualFold(file.Extension, ".flac") && file.Size > largeFLACWarningBytes
	return progressDisplayActivity{title: "Currently processing", path: file.RelativePath, warning: warning}
}

func (p *pipelineProgress) SetCurrentOperation(name string) {
	p.setActivity(progressDisplayActivity{title: "Current operation", path: name})
}

// SetCurrentDirectory keeps long directory enumeration visibly alive before an
// audio file is discovered. It displays the logical root alias rather than an
// absolute source path.
func (p *pipelineProgress) SetCurrentDirectory(name string) {
	p.setActivity(progressDisplayActivity{title: "Scanning directory", path: name})
}

func (p *pipelineProgress) setActivity(activity progressDisplayActivity) {
	if p == nil {
		return
	}
	if activity.started.IsZero() {
		activity.started = time.Now()
	}
	select {
	case p.current <- activity:
	default:
		select {
		case <-p.current:
		default:
		}
		select {
		case p.current <- activity:
		default:
		}
	}
}

// Stop is idempotent. A successful stop fills the bar; an unsuccessful stop
// preserves the last durable count and labels the operation as stopped.
func (p *pipelineProgress) Stop(success bool) {
	if p == nil {
		return
	}
	p.once.Do(func() { p.stop <- success })
	<-p.done
}

func (p *pipelineProgress) run(ctx context.Context, state progressSnapshotReader, semanticJobs map[string]string, bar *pterm.ProgressbarPrinter, barOutput *bytes.Buffer, area *cursor.Area, phase string, mode progressMode) {
	defer close(p.done)
	defer cursor.SetTarget(os.Stdout)
	ticker := time.NewTicker(progressRefreshInterval)
	defer ticker.Stop()
	last := libraryindex.ProgressSnapshot{}
	activity := progressDisplayActivity{}
	lastActivitySecond := int64(-1)
	barLine := latestProgressLine(barOutput)
	summaryLine := progressTitle(phase, last, mode)
	lastBarRedraw := time.Time{}
	var eta analysisETA
	var lastETASample time.Time
	// Analysis status lines carry a completion estimate; scan and export
	// phases have no finished-file throughput to extrapolate.
	title := func(phase string, snapshot libraryindex.ProgressSnapshot) string {
		line := progressTitle(phase, snapshot, mode)
		if mode == progressJobs {
			line += eta.suffix(snapshot.Total - snapshot.Finished)
		}
		return line
	}
	updateArea := func() {
		content := summaryLine + "\n" + barLine
		if mode != progressActivity {
			title, detail := progressActivityDisplay(activity, phase, last)
			content = progressActivityBox(title, detail, activity.warning) + "\n" + content
		}
		area.Update(strings.TrimRight(content, "\n") + "\n")
		lastActivitySecond = progressActivityAge(activity, time.Now())
	}
	redrawBar := func(snapshot libraryindex.ProgressSnapshot, now time.Time) {
		line := updateProgressBar(bar, barOutput, snapshot, mode)
		if line != "" {
			barLine = line
		}
		lastBarRedraw = now
	}
	show := func(snapshot libraryindex.ProgressSnapshot, ok, force bool) {
		now := time.Now()
		if !ok {
			// Activity updates must not depend on a read connection becoming
			// available while the durable writer is busy.
			summaryLine = title(phase, last)
			redrawDue := progressRedrawDue(lastBarRedraw, now)
			if force || redrawDue {
				redrawBar(last, now)
			}
			if force || progressActivityAge(activity, now) != lastActivitySecond || redrawDue {
				updateArea()
			}
			return
		}
		// Sample at most once per second, including unchanged snapshots, so a
		// stall lowers the measured throughput instead of freezing the ETA.
		if now.Sub(lastETASample) >= time.Second {
			eta.observe(now, snapshot.Finished)
			lastETASample = now
		}
		snapshotChanged := snapshot != last
		activityChanged := progressActivityAge(activity, now) != lastActivitySecond
		redrawDue := progressRedrawDue(lastBarRedraw, now)
		if !force && !snapshotChanged && !activityChanged && !redrawDue {
			return
		}
		last = snapshot
		summaryLine = title(phase, snapshot)
		if force || snapshotChanged || redrawDue {
			redrawBar(snapshot, now)
		}
		updateArea()
	}
	// Exactly one poller owns durable reads. Rendering and shutdown always use
	// the cached snapshot, even when SQLite is waiting on a busy connection.
	render := func(force bool) { show(last, false, force) }
	polled := make(chan polledProgressSnapshot, 1)
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	generation := func() progressGeneration { return p.snapshotGeneration(state) }
	go pollProgressSnapshots(pollCtx, state, semanticJobs, polled, generation)
	render(true)
	for {
		select {
		case next := <-p.phase:
			phase = next
			render(true)
		case activity = <-p.current:
			// File discovery can produce tens of thousands of activity events.
			// Redraw the transient activity immediately, but leave the durable
			// inventory count to the once-per-second ticker so display work does
			// not turn scanning into a count query per file.
			updateArea()
		case success := <-p.stop:
			render(true)
			if success {
				bar.Total = max(1, bar.Total)
				bar.Current = bar.Total
				summaryLine = progressTitle("Complete", last, mode)
			} else {
				summaryLine = progressTitle("Stopped", last, mode)
			}
			if line := refreshProgressBar(bar, barOutput); line != "" {
				barLine = line
			}
			updateArea()
			_, _ = bar.Stop()
			return
		case <-ctx.Done():
			render(true)
			summaryLine = progressTitle("Interrupted", last, mode)
			if line := refreshProgressBar(bar, barOutput); line != "" {
				barLine = line
			}
			updateArea()
			_, _ = bar.Stop()
			return
		case snapshot := <-polled:
			if snapshot.generation == generation() {
				show(snapshot.snapshot, true, false)
			}
		case <-ticker.C:
			// Advance the activity age and redraw deadline between polls.
			show(last, true, false)
		}
	}
}

func pollProgressSnapshots(ctx context.Context, state progressSnapshotReader, semanticJobs map[string]string, out chan polledProgressSnapshot, generation func() progressGeneration) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		stamp := generation()
		started := time.Now()
		queryCtx, cancel := context.WithTimeout(ctx, progressQueryTimeout)
		snapshot, err := state.Progress(queryCtx, semanticJobs)
		cancel()
		wait = max(progressRefreshInterval, progressQueryBudget*time.Since(started))
		if err != nil || ctx.Err() != nil || stamp != generation() {
			continue
		}
		// This goroutine is the only sender, so draining keeps just the newest
		// snapshot and the following send cannot block.
		select {
		case <-out:
		default:
		}
		out <- polledProgressSnapshot{snapshot: snapshot, generation: stamp}
	}
}

func progressActivityBox(title, detail string, warning bool) string {
	boxPrinter := pterm.DefaultBox.WithTitle(title)
	if warning {
		boxPrinter = boxPrinter.WithTextStyle(pterm.NewStyle(pterm.FgRed))
	}
	return boxPrinter.Sprint(detail)
}

func updateProgressBar(bar *pterm.ProgressbarPrinter, output *bytes.Buffer, snapshot libraryindex.ProgressSnapshot, mode progressMode) string {
	total, current := int64(1), int64(0)
	if mode == progressScan {
		total = max(int64(1), snapshot.Files)
		current = snapshot.Files
	} else if mode != progressActivity {
		total = max(int64(1), snapshot.Total)
	}
	if mode == progressJobs {
		current = min(snapshot.Finished, total)
	}
	bar.Total = boundedProgressInt(total)
	bar.Current = boundedProgressInt(current)
	return refreshProgressBar(bar, output)
}

func refreshProgressBar(bar *pterm.ProgressbarPrinter, output *bytes.Buffer) string {
	bar.UpdateTitle("")
	return latestProgressLine(output)
}

func progressRedrawDue(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= progressFullRedraw
}

func latestProgressLine(output *bytes.Buffer) string {
	contents := output.String()
	output.Reset()
	parts := strings.Split(contents, "\r")
	for index := len(parts) - 1; index >= 0; index-- {
		if line := strings.Trim(parts[index], "\n\r"); line != "" {
			return line
		}
	}
	return ""
}

func progressFileName(name string) string {
	quoted := strconv.QuoteToGraphic(name)
	if len(quoted) >= 2 {
		quoted = quoted[1 : len(quoted)-1]
	}
	runes := []rune(quoted)
	const maxRunes = 96
	if len(runes) > maxRunes {
		quoted = string(runes[:maxRunes-1]) + "…"
	}
	return quoted
}

func progressActivityDisplay(activity progressDisplayActivity, phase string, snapshot libraryindex.ProgressSnapshot) (string, string) {
	return progressActivityDisplayAt(activity, phase, snapshot, time.Now())
}

func progressActivityDisplayAt(activity progressDisplayActivity, phase string, snapshot libraryindex.ProgressSnapshot, now time.Time) (string, string) {
	if activity.path != "" {
		detail := progressFileName(activity.path)
		if age := progressActivityAge(activity, now); age > 0 {
			detail += fmt.Sprintf("\nActive for %s", (time.Duration(age) * time.Second).String())
		}
		return activity.title, detail
	}
	if strings.Contains(strings.ToLower(phase), "scann") {
		return "Current activity", "Scanning directory inventory…"
	}
	if snapshot.Queued > 0 {
		return "Current activity", "Waiting for an available worker…"
	}
	return "Current activity", "Waiting for discovered work…"
}

func progressActivityAge(activity progressDisplayActivity, now time.Time) int64 {
	if activity.started.IsZero() || now.Before(activity.started) {
		return -1
	}
	return int64(now.Sub(activity.started) / time.Second)
}

func progressTitle(phase string, snapshot libraryindex.ProgressSnapshot, mode progressMode) string {
	if mode == progressScan {
		if phase == "Complete" {
			return fmt.Sprintf("Complete • %d audio files discovered • %d queued for processing", snapshot.Files, snapshot.Total)
		}
		return fmt.Sprintf("%s • %d audio files discovered • %d queued for processing", phase, snapshot.Files, snapshot.Total)
	}
	if mode == progressActivity {
		return fmt.Sprintf("%s • %d library files", phase, snapshot.Files)
	}
	title := fmt.Sprintf("%s • %d audio files • %d queued • %d active • %d/%d finished", phase, snapshot.Files, snapshot.Queued, snapshot.Leased, snapshot.Finished, snapshot.Total)
	if snapshot.Failed > 0 {
		title += fmt.Sprintf(" • %d failed", snapshot.Failed)
	}
	if snapshot.Retries > 0 {
		title += fmt.Sprintf(" • %d retries", snapshot.Retries)
	}
	return title
}

func boundedProgressInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	if value < 0 {
		return 0
	}
	return int(value)
}
