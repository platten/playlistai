package bridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// ProgressEventName is the Wails event the frontend subscribes to for progress.
const ProgressEventName = "playlistai:progress"

// ProgressEvent is the payload delivered to the frontend on each report.
type ProgressEvent struct {
	GenerationID   string         `json:"generationId"`
	SuggestedTrack *core.TrackRef `json:"suggestedTrack,omitempty"`
	CheckedTrack   *core.TrackRef `json:"checkedTrack,omitempty"`
	Op             string         `json:"op"`
	Done           int64          `json:"done"`
	Total          int64          `json:"total"` // <= 0 => indeterminate
	Note           string         `json:"note"`
}

// WailsProgress implements ports.Progress by emitting ProgressEventName events
// via the running Wails application. If no application is running (tests,
// headless) Report is a no-op.
type WailsProgress struct{ generationID string }

// NewWailsProgress returns a Progress reporter that emits frontend events.
func NewWailsProgress() *WailsProgress { return &WailsProgress{} }

// Report implements ports.Progress.
func (p *WailsProgress) Report(op string, done, total int64, note string) {
	appInst := application.Get()
	if appInst == nil {
		return
	}
	appInst.Event.Emit(ProgressEventName, ProgressEvent{
		GenerationID: p.generationID,
		Op:           op,
		Done:         done,
		Total:        total,
		Note:         note,
	})
}

func (p *WailsProgress) Checked(track core.TrackRef) {
	if appInst := application.Get(); appInst != nil {
		appInst.Event.Emit(ProgressEventName, ProgressEvent{GenerationID: p.generationID, Op: "generation", Note: "Checking musical fit", CheckedTrack: &track})
	}
}

var _ ports.Progress = (*WailsProgress)(nil)

func (p *WailsProgress) Suggested(track core.TrackRef) {
	if appInst := application.Get(); appInst != nil {
		appInst.Event.Emit(ProgressEventName, ProgressEvent{GenerationID: p.generationID, Op: "generation", Note: "Finding musical suggestions", SuggestedTrack: &track})
	}
}
