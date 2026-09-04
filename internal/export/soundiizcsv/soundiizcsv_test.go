package soundiizcsv

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func req(name string, tracks ...core.EnrichedTrack) ports.ExportRequest {
	return ports.ExportRequest{Name: name, Tracks: tracks}
}

func TestExportCSV(t *testing.T) {
	t.Parallel()
	rec := &fakes.RecordingProgress{}
	res, err := New().Export(context.Background(), req("Justice — 90s, no vocals!",
		core.EnrichedTrack{Ref: core.TrackRef{Artist: "Justice", Title: "Genesis"}, Album: "Cross", ISRC: "FRV840700010"},
		core.EnrichedTrack{Ref: core.TrackRef{Artist: "The Chemical Brothers", Title: "Star Guitar, Radio Edit"}, AllArtists: []string{"The Chemical Brothers"}},
		core.EnrichedTrack{Ref: core.TrackRef{Artist: `Say "Hi"`, Title: "Quote \"me\""}},
	), rec)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Kind != "csv" || res.Count != 3 {
		t.Fatalf("result = %+v", res)
	}
	if res.Location != "Justice 90s no vocals.csv" {
		t.Fatalf("filename = %q, want %q", res.Location, "Justice 90s no vocals.csv")
	}

	rows, err := csv.NewReader(bytes.NewReader(res.Data)).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0][0] != "title" || rows[0][3] != "isrc" {
		t.Fatalf("header = %v", rows[0])
	}
	if rows[1][0] != "Genesis" || rows[1][1] != "Justice" || rows[1][2] != "Cross" || rows[1][3] != "FRV840700010" {
		t.Fatalf("row 1 = %v", rows[1])
	}
	// comma + quotes in a field must survive the round trip
	if rows[3][0] != `Quote "me"` || rows[3][1] != `Say "Hi"` {
		t.Fatalf("quoting broken: %v", rows[3])
	}

	if got := rec.Snapshot(); len(got) != 3 || got[2].Done != 3 {
		t.Fatalf("progress = %+v", got)
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                    "playlist",
		"  ///  ":             "playlist",
		"Road/Trip: 2024":     "Road Trip 2024",
		"already-fine_name.1": "already-fine_name.1",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAvailableAndName(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.Available() || e.Name() != "csv" {
		t.Fatalf("Available=%v Name=%q", e.Available(), e.Name())
	}
}
