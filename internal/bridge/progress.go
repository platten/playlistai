package bridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/ports"
)

// ProgressEventName is the Wails event the frontend subscribes to for progress.
const ProgressEventName = "playlistai:progress"

// ProgressEvent is the payload delivered to the frontend on each report.
type ProgressEvent struct {
	Op    string `json:"op"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"` // <= 0 => indeterminate
	Note  string `json:"note"`
}

// WailsProgress implements ports.Progress by emitting ProgressEventName events
// via the running Wails application. If no application is running (tests,
// headless) Report is a no-op.
type WailsProgress struct{}

// NewWailsProgress returns a Progress reporter that emits frontend events.
func NewWailsProgress() *WailsProgress { return &WailsProgress{} }

// Report implements ports.Progress.
func (*WailsProgress) Report(op string, done, total int64, note string) {
	appInst := application.Get()
	if appInst == nil {
		return
	}
	appInst.Event.Emit(ProgressEventName, ProgressEvent{
		Op:    op,
		Done:  done,
		Total: total,
		Note:  note,
	})
}

var _ ports.Progress = (*WailsProgress)(nil)
