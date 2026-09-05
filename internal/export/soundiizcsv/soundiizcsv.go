// Package soundiizcsv writes a playlist as a CSV that Soundiiz's file importer
// accepts (columns: title, artist, album, isrc, duration). No network, always
// available.
package soundiizcsv

import (
	"bytes"
	"context"
	"encoding/csv"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/ports"
)

// ProgressOp is the op label for export progress reports.
const ProgressOp = "export"

// Exporter implements ports.Exporter.
type Exporter struct{}

// New returns a CSV exporter.
func New() *Exporter { return &Exporter{} }

// Name implements ports.Exporter.
func (*Exporter) Name() string { return "csv" }

// Available implements ports.Exporter.
func (*Exporter) Available() bool { return true }

// Export builds the CSV. ExportResult.Data holds the bytes and Location is a
// suggested filename.
func (*Exporter) Export(_ context.Context, req ports.ExportRequest, p ports.Progress) (ports.ExportResult, error) {
	if p == nil {
		p = ports.NopProgress{}
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"title", "artist", "album", "isrc", "duration"}); err != nil {
		return ports.ExportResult{}, err
	}

	n := int64(len(req.Tracks))
	for i, t := range req.Tracks {
		artist := t.Ref.Artist
		if len(t.AllArtists) > 0 {
			artist = strings.Join(t.AllArtists, ", ")
		}
		if err := w.Write([]string{t.Ref.Title, artist, t.Album, t.ISRC, ""}); err != nil {
			return ports.ExportResult{}, err
		}
		p.Report(ProgressOp, int64(i+1), n, t.Ref.Title)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return ports.ExportResult{}, err
	}

	return ports.ExportResult{
		Kind:     "csv",
		Location: EnsureCSVExt(sanitizeFilename(req.Name)),
		Data:     buf.Bytes(),
		Count:    len(req.Tracks),
	}, nil
}

// EnsureCSVExt returns name with a ".csv" extension, adding one only when it
// isn't already there (case-insensitive), so "list" -> "list.csv" but
// "list.csv" and "list.CSV" are left as-is.
func EnsureCSVExt(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".csv") {
		return name
	}
	return name + ".csv"
}

var unsafeName = regexp.MustCompile(`[^\w \-.]+`)

func sanitizeFilename(name string) string {
	s := strings.TrimSpace(unsafeName.ReplaceAllString(name, " "))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "playlist"
	}
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80])
	}
	return s
}

var _ ports.Exporter = (*Exporter)(nil)
