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
	"time"

	"atomicgo.dev/cursor"
	"github.com/pterm/pterm"
	"golang.org/x/term"

	"github.com/platten/playlistai/internal/libraryindex"
)

const progressRefreshInterval = 250 * time.Millisecond

type progressMode uint8

const (
	progressJobs progressMode = iota
	progressScan
	progressActivity
)

type pipelineProgress struct {
	phase   chan string
	current chan string
	stop    chan bool
	done    chan struct{}
	once    sync.Once
}

func startPipelineProgress(ctx context.Context, state *libraryindex.State, semanticJobs map[string]string, writer io.Writer, disabled bool, initialPhase string, mode progressMode) *pipelineProgress {
	progress := &pipelineProgress{phase: make(chan string, 1), current: make(chan string, 1), stop: make(chan bool, 1), done: make(chan struct{})}
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
		WithShowElapsedTime(false).
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

func (p *pipelineProgress) SetPhase(phase string) {
	if p == nil || phase == "" {
		return
	}
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
func (p *pipelineProgress) SetCurrentFile(name string) {
	if p == nil {
		return
	}
	select {
	case p.current <- name:
	default:
		select {
		case <-p.current:
		default:
		}
		select {
		case p.current <- name:
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

func (p *pipelineProgress) run(ctx context.Context, state *libraryindex.State, semanticJobs map[string]string, bar *pterm.ProgressbarPrinter, barOutput *bytes.Buffer, area *cursor.Area, phase string, mode progressMode) {
	defer close(p.done)
	defer cursor.SetTarget(os.Stdout)
	ticker := time.NewTicker(progressRefreshInterval)
	defer ticker.Stop()
	last := libraryindex.ProgressSnapshot{Total: -1}
	currentFile := ""
	barLine := latestProgressLine(barOutput)
	updateArea := func() {
		content := barLine
		if mode != progressActivity {
			box := pterm.DefaultBox.WithTitle("Currently processing").Sprint(progressFileName(currentFile))
			content = box + "\n" + barLine
		}
		area.Update(strings.TrimRight(content, "\n") + "\n")
	}
	render := func(force bool) {
		queryCtx, cancel := context.WithTimeout(context.Background(), progressRefreshInterval)
		snapshot, err := state.Progress(queryCtx, semanticJobs)
		cancel()
		if err != nil {
			return
		}
		if !force && snapshot == last {
			return
		}
		last = snapshot
		total, current := int64(1), int64(0)
		if mode == progressJobs {
			total = max(int64(1), snapshot.Total)
			current = min(snapshot.Finished, total)
		}
		bar.Total = boundedProgressInt(total)
		bar.Current = boundedProgressInt(current)
		bar.UpdateTitle(progressTitle(phase, snapshot, mode))
		barLine = latestProgressLine(barOutput)
		updateArea()
	}
	render(true)
	for {
		select {
		case next := <-p.phase:
			phase = next
			render(true)
		case currentFile = <-p.current:
			render(true)
		case success := <-p.stop:
			render(true)
			if success {
				bar.Total = max(1, bar.Total)
				bar.Current = bar.Total
				bar.UpdateTitle(progressTitle("Complete", last, mode))
			} else {
				bar.UpdateTitle(progressTitle("Stopped", last, mode))
			}
			barLine = latestProgressLine(barOutput)
			updateArea()
			_, _ = bar.Stop()
			return
		case <-ctx.Done():
			render(true)
			bar.UpdateTitle(progressTitle("Interrupted", last, mode))
			barLine = latestProgressLine(barOutput)
			updateArea()
			_, _ = bar.Stop()
			return
		case <-ticker.C:
			render(false)
		}
	}
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
	if name == "" {
		return "Waiting for a file…"
	}
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

func progressTitle(phase string, snapshot libraryindex.ProgressSnapshot, mode progressMode) string {
	if mode == progressScan {
		if phase == "Complete" {
			return fmt.Sprintf("Complete • %d files discovered • %d jobs queued for analysis", snapshot.Files, snapshot.Queued)
		}
		return fmt.Sprintf("%s • %d files discovered • %d jobs queued", phase, snapshot.Files, snapshot.Queued)
	}
	if mode == progressActivity {
		return fmt.Sprintf("%s • %d library files", phase, snapshot.Files)
	}
	title := fmt.Sprintf("%s • %d files • %d queued • %d active • %d/%d finished", phase, snapshot.Files, snapshot.Queued, snapshot.Leased, snapshot.Finished, snapshot.Total)
	if snapshot.Failed > 0 {
		title += fmt.Sprintf(" • %d failed", snapshot.Failed)
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
