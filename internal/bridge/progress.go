package bridge

import (
	"context"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

// ProgressEventName is the Wails event the frontend subscribes to for progress.
const ProgressEventName = "playlistai:progress"

// ProgressEvent is the payload delivered to the frontend on each report.
type ProgressEvent struct {
	core.SearchProgress
	GenerationID   string         `json:"generationId"`
	SuggestedTrack *core.TrackRef `json:"suggestedTrack,omitempty"`
	CheckedTrack   *core.TrackRef `json:"checkedTrack,omitempty"`
	Op             string         `json:"op"`
	Done           int64          `json:"done"`
	Total          int64          `json:"total"` // <= 0 => indeterminate
	Note           string         `json:"note"`
}

// WailsProgress implements ports.Progress by emitting ProgressEventName events
// via the running Wails application. Generation reporters also retain safe
// progress fields in the existing opt-in diagnostic sink, including headless.
type WailsProgress struct {
	ctx          context.Context
	generationID string
	started      time.Time
	mu           sync.Mutex
	search       core.SearchProgress
}

// NewWailsProgress returns a Progress reporter that emits frontend events.
func NewWailsProgress() *WailsProgress { return &WailsProgress{} }

// Report implements ports.Progress.
func (p *WailsProgress) Report(op string, done, total int64, note string) {
	p.mu.Lock()
	if op == "intent" {
		p.search.Stage = "parsing"
	} else if op == "generation" && p.search.Stage == "" {
		p.search.Stage = "retrieval"
	}
	p.mu.Unlock()
	p.emit(ProgressEvent{
		GenerationID: p.generationID,
		Op:           op,
		Done:         done,
		Total:        total,
		Note:         note,
	})
}

func (p *WailsProgress) emit(event ProgressEvent) {
	p.mu.Lock()
	event.SearchProgress = p.search
	p.mu.Unlock()
	if !p.started.IsZero() {
		event.ElapsedMilliseconds = time.Since(p.started).Milliseconds()
	}
	event.GenerationID = p.generationID
	if p.ctx != nil {
		// Notes and track payloads can contain user references. Progress needs
		// only phase, counts and timing; detailed diagnostics remain separate.
		logging.Diagnostic(p.ctx, "generation.progress", struct {
			core.SearchProgress
			Op        string `json:"op"`
			Done      int64  `json:"done"`
			Total     int64  `json:"total"`
			Checked   bool   `json:"checked,omitempty"`
			Suggested bool   `json:"suggested,omitempty"`
		}{event.SearchProgress, event.Op, event.Done, event.Total, event.CheckedTrack != nil, event.SuggestedTrack != nil})
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(ProgressEventName, event)
	}
}

func (p *WailsProgress) Search(state core.SearchProgress) {
	p.mu.Lock()
	p.search = state
	p.mu.Unlock()
	note := "Checking musical fit"
	switch state.Stage {
	case "verifying":
		note = "Checking recording sources"
	case "comparing":
		note = "Comparing matches"
	case "assembling":
		note = "Ordering your playlist"
	case "complete":
		note = "Comparison complete"
	}
	p.emit(ProgressEvent{Op: "generation", Note: note})
}

func (p *WailsProgress) Checked(track core.TrackRef) {
	p.emit(ProgressEvent{Op: "generation", Note: "Checking musical fit", CheckedTrack: &track})
}

var _ ports.Progress = (*WailsProgress)(nil)

func (p *WailsProgress) Suggested(track core.TrackRef) {
	p.emit(ProgressEvent{Op: "generation", Note: "Finding musical suggestions", SuggestedTrack: &track})
}
