package bridge

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/platten/playlistai/internal/ports"
)

// ProgressEventName is the Wails event the frontend subscribes to for progress.
const ProgressEventName = "progress"

// ProgressEvent is the payload delivered to the frontend on each report.
type ProgressEvent struct {
	Op    string `json:"op"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"` // <= 0 => indeterminate
	Note  string `json:"note"`
}

// WailsProgress implements ports.Progress by emitting ProgressEventName events.
// A nil receiver or nil context makes Report a no-op, so it is safe to use
// before the Wails runtime has started.
type WailsProgress struct {
	ctx context.Context
}

// NewWailsProgress binds a Progress reporter to a Wails context.
func NewWailsProgress(ctx context.Context) *WailsProgress {
	return &WailsProgress{ctx: ctx}
}

// Report implements ports.Progress.
func (w *WailsProgress) Report(op string, done, total int64, note string) {
	if w == nil || w.ctx == nil {
		return
	}
	wruntime.EventsEmit(w.ctx, ProgressEventName, ProgressEvent{
		Op:    op,
		Done:  done,
		Total: total,
		Note:  note,
	})
}

var _ ports.Progress = (*WailsProgress)(nil)
