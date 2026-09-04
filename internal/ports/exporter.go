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
	Kind     string // "soundiiz-handoff" | "csv"
	Location string // for the handoff: the validated shareUrl to open in the browser
	Data     []byte // populated for file exports the UI must hand to the user
	Count    int    // tracks exported
}

// Exporter turns a playlist into something that leaves the app. Two production
// implementations:
//
//   - csv: writes a Soundiiz-compatible CSV for the UI to save. Always available,
//     no network.
//   - soundiiz-handoff: POSTs {title, sourceName, description, tracklist} to
//     https://soundiiz.com/go/import-playlist (no token), validates the returned
//     shareUrl against the fixed host+path prefix, and returns it for the UI to
//     open in the browser. Needs network at call time; Available() reports that.
type Exporter interface {
	Export(ctx context.Context, req ExportRequest, p Progress) (ExportResult, error)
	Name() string
	Available() bool
}
