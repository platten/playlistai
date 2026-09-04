package ports

// Progress receives coarse updates from long-running operations so the UI can
// show a bar. A total <= 0 means the total is unknown: render an indeterminate
// bar plus the note as a status line.
//
// Implementations must be safe for concurrent use and must never block the
// caller (drop or coalesce if a consumer is slow).
type Progress interface {
	Report(op string, done, total int64, note string)
}

// NopProgress discards every report. Use it in tests and for operations invoked
// outside a UI context.
type NopProgress struct{}

// Report implements Progress.
func (NopProgress) Report(string, int64, int64, string) {}

// ProgressFunc adapts a function to the Progress interface.
type ProgressFunc func(op string, done, total int64, note string)

// Report implements Progress.
func (f ProgressFunc) Report(op string, done, total int64, note string) {
	if f != nil {
		f(op, done, total, note)
	}
}
