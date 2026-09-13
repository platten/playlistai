package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pterm/pterm"
)

type progressState struct {
	label       string
	done        int64
	total       int64
	rows        int64
	unit        string
	started     time.Time
	finished    time.Time
	initialDone int64
	initialized bool
}

type progressDisplay struct {
	mu          sync.Mutex
	out         io.Writer
	states      map[string]*progressState
	order       []string
	interactive bool
	rendered    int
	stop        chan struct{}
	finished    chan struct{}
	closeOnce   sync.Once
}

func newProgressDisplay(out io.Writer) *progressDisplay {
	p := &progressDisplay{out: out, states: map[string]*progressState{}, stop: make(chan struct{}), finished: make(chan struct{})}
	if file, ok := out.(*os.File); ok {
		if info, err := file.Stat(); err == nil {
			p.interactive = info.Mode()&os.ModeCharDevice != 0
		}
	}
	interval := 10 * time.Second
	if p.interactive {
		interval = time.Second
	}
	go func() {
		defer close(p.finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				p.render(now)
			case <-p.stop:
				p.render(time.Now())
				return
			}
		}
	}()
	return p
}

func (p *progressDisplay) Add(key, label, unit string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.states[key]; exists {
		return
	}
	p.states[key] = &progressState{label: label, unit: unit}
	p.order = append(p.order, key)
}

func (p *progressDisplay) Update(key, label string, done, total, rows int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, exists := p.states[key]
	if !exists {
		state = &progressState{}
		p.states[key] = state
		p.order = append(p.order, key)
	}
	if label != "" {
		state.label = label
	}
	if !state.initialized {
		state.started = time.Now()
		state.initialDone = done
		state.initialized = true
	}
	state.done, state.total, state.rows = done, total, rows
	if total > 0 && done >= total && state.finished.IsZero() {
		state.finished = time.Now()
	}
}

func (p *progressDisplay) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		<-p.finished
	})
}

func (p *progressDisplay) render(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) == 0 {
		return
	}
	if p.interactive && p.rendered > 0 {
		fmt.Fprintf(p.out, "\x1b[%dA", p.rendered)
	}
	rendered := 0
	for _, key := range p.order {
		if !p.states[key].initialized {
			continue
		}
		rendered++
		line := formatProgress(*p.states[key], now)
		if p.interactive {
			fmt.Fprintf(p.out, "\r\x1b[2K%s\n", line)
		} else {
			fmt.Fprintln(p.out, line)
		}
	}
	p.rendered = rendered
}

func formatProgress(state progressState, now time.Time) string {
	done := max(int64(0), state.done)
	total := max(int64(0), state.total)
	ratio := 0.0
	if total > 0 {
		ratio = min(1, float64(done)/float64(total))
	}
	// Render pterm synchronously into this frame. Its live Start/Stop methods
	// manipulate the global terminal cursor and its timer cannot share our lock.
	// Owning rendering here keeps concurrent callbacks safe and stdout JSON clean.
	var buffer strings.Builder
	printer := pterm.DefaultProgressbar.WithWriter(&buffer).WithTotal(10000).
		WithCurrent(int(ratio * 10000)).WithMaxWidth(34).
		WithShowElapsedTime(false).WithShowTitle(false).WithShowCount(false)
	printer.BarFiller = "░"
	printer.IsActive = true
	printer.UpdateTitle("")
	bar := strings.TrimSpace(pterm.RemoveColorFromString(buffer.String()))
	amount := fmt.Sprintf("%s / %s", formatBytes(done), formatBytes(total))
	if state.unit != "bytes" {
		amount = fmt.Sprintf("%d / %d %s", done, total, state.unit)
	}
	line := fmt.Sprintf("%-22s %s  %s", state.label, bar, amount)
	if state.initialized {
		elapsed := now.Sub(state.started)
		if !state.finished.IsZero() {
			elapsed = state.finished.Sub(state.started)
		}
		advanced := done - state.initialDone
		if done >= total && total > 0 {
			line += "  done in " + formatClock(elapsed)
		} else if advanced > 0 && elapsed > 0 && total > done {
			rate := float64(advanced) / elapsed.Seconds()
			eta := time.Duration(float64(total-done) / rate * float64(time.Second))
			speed := fmt.Sprintf("%.1f %s/s", rate, state.unit)
			if state.unit == "bytes" {
				speed = formatBytes(int64(rate)) + "/s"
			}
			line += fmt.Sprintf("  %s  ETA %s", speed, formatClock(eta))
		}
	}
	if state.rows > 0 {
		line += "  " + formatCount(state.rows) + " rows"
	}
	return line
}

func formatCount(value int64) string {
	raw := fmt.Sprintf("%d", value)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	return raw
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, suffix := float64(unit), "KiB"
	for _, next := range []string{"MiB", "GiB", "TiB"} {
		if float64(value) < divisor*unit {
			break
		}
		divisor *= unit
		suffix = next
	}
	return fmt.Sprintf("%.1f %s", float64(value)/divisor, suffix)
}

func formatClock(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	value = value.Round(time.Second)
	hours := int(value / time.Hour)
	minutes := int(value%time.Hour) / int(time.Minute)
	seconds := int(value%time.Minute) / int(time.Second)
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
