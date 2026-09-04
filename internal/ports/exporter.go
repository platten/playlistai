package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// ExportRequest is a named playlist ready to leave the app.
type ExportRequest struct {
	Name   string
	Tracks []core.EnrichedTrack
}

// ExportResult is the outcome of an export.
type ExportResult struct {
	Kind     string // "soundiiz-api" | "csv"
	Location string // remote playlist URL, or a suggested local filename
	Data     []byte // populated for file exports the UI must hand to the user
	Count    int    // tracks exported
}

// Exporter sends a playlist to an external destination. The CSV exporter always
// works; the Soundiiz API exporter requires a configured token and reports
// Available() == false otherwise (Export then returns core.ErrUnavailable).
type Exporter interface {
	Export(ctx context.Context, req ExportRequest, p Progress) (ExportResult, error)
	Name() string
	Available() bool
}
