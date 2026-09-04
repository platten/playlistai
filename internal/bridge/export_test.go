package bridge

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestEnrichPlaylistErrors(t *testing.T) {
	t.Parallel()

	// Bare container: no catalog, no enricher.
	bare := New(newTestContainer(t), nil)
	if _, err := bare.EnrichPlaylist([]string{"x"}); err == nil {
		t.Fatal("expected an error with no catalog/enricher")
	}

	// Loaded container but only unknown ids: resolves to an empty list with no
	// network call and no error.
	loaded := New(newLoadedContainer(t), nil)
	got, err := loaded.EnrichPlaylist([]string{"not-a-real-id", "also-fake"})
	if err != nil {
		t.Fatalf("unknown ids should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown ids should resolve to nothing, got %d", len(got))
	}
}

func TestExportCSVFallbackPath(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)

	rows := []EnrichedTrackDTO{
		{ID: "seed0001", Artist: "Justice", Title: "Genesis", ISRC: "FRV840700010", Album: "Cross"},
		{ID: "seed0003", Artist: "SebastiAn", Title: "Rerun", AllArtists: []string{"SebastiAn", "Mr Oizo"}},
	}

	res, err := api.ExportCSV("My Mix / 2026", rows)
	if err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	if res.Canceled || res.Count != 2 {
		t.Fatalf("result = %+v", res)
	}

	wantDir := filepath.Join(c.Config().DataDir, "exports")
	if filepath.Dir(res.Path) != wantDir {
		t.Fatalf("path %q not under %q", res.Path, wantDir)
	}
	if filepath.Base(res.Path) != "My Mix 2026.csv" {
		t.Fatalf("filename = %q", filepath.Base(res.Path))
	}

	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("export is not valid CSV: %v", err)
	}
	if len(recs) != 3 || recs[0][0] != "title" {
		t.Fatalf("rows = %v", recs)
	}
	if recs[1][3] != "FRV840700010" || recs[2][1] != "SebastiAn, Mr Oizo" {
		t.Fatalf("row content = %v", recs[1:])
	}
}

func TestDTORoundTrip(t *testing.T) {
	t.Parallel()
	in := core.EnrichedTrack{
		Ref:        core.TrackRef{ID: "id1", Artist: "A", Title: "B"},
		Matched:    true,
		ISRC:       "US1234567890",
		AllISRCs:   []string{"US1234567890", "GB0987654321"},
		Album:      "Album",
		Year:       2019,
		AllArtists: []string{"A", "C"},
		MatchScore: 92,
	}
	out := enrichedFromDTO(dtoFromEnriched(in))
	if out.Ref != in.Ref || out.ISRC != in.ISRC || out.Year != in.Year ||
		out.MatchScore != in.MatchScore || out.Album != in.Album ||
		len(out.AllISRCs) != 2 || len(out.AllArtists) != 2 || !out.Matched {
		t.Fatalf("round trip lost data: %+v", out)
	}
}
