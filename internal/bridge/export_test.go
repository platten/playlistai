package bridge

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/librarypack"
)

func importBridgeLibraryTrack(t *testing.T, c *app.Container) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.paipack")
	_, err := librarypack.Write(context.Background(), path, librarypack.Pack{
		CorpusGeneration: "bridge", MetadataGeneration: "bridge-metadata",
		MERTGeneration: "bridge-mert",
		MERT: librarypack.VectorSpace{Name: "library_mert", Dimension: 3, DType: "float32", ByteOrder: "little", Normalized: true,
			Model: "MERT-v1-95M", ModelRevision: "fixture", GraphSHA256: strings.Repeat("b", 64), Decoder: "fixture",
			Preprocessing: "mert-mono-24k-v1", Sampling: "balanced-v1", Pooling: "final-mean-l2-v1", Scope: "sampled-excerpts", Missingness: "absent-row"},
		Tracks: []librarypack.Track{{ID: "ambient", Artist: "Local Artist", Title: "Gentle Pulse", Album: "Local Album", MERT: []float32{1, 0, 0}}},
	}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryasset.BuildIndexedFromPack(context.Background(), path, path, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportLocalLibrary(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	local, err := c.PinLocalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	return local.NamespacedID("ambient")
}

func TestPrepareExportErrors(t *testing.T) {
	t.Parallel()

	// Bare container: no catalog, no enricher.
	bare := New(newTestContainer(t), nil)
	if _, err := bare.PrepareExport([]string{"x"}); err == nil {
		t.Fatal("expected an error with no catalog")
	}

	// Loaded container but only unknown ids: resolves to an empty list with no
	// network call and no error.
	loaded := New(newLoadedContainer(t), nil)
	got, err := loaded.PrepareExport([]string{"not-a-real-id", "also-fake"})
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

	c.Enrich = nil
	rows := []ExportTrackDTO{
		{ID: "seed0001", Artist: "Justice", Title: "Genesis", Album: "Cross"},
		{ID: "seed0003", Artist: "SebastiAn, Mr Oizo", Title: "Rerun"},
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
	if recs[1][3] != "" || recs[1][2] != "Cross" || recs[2][1] != "SebastiAn, Mr Oizo" {
		t.Fatalf("row content = %v", recs[1:])
	}
}

func TestExportCSVAlwaysHasCSVExtension(t *testing.T) {
	t.Parallel()
	rows := []ExportTrackDTO{{ID: "seed0001", Artist: "Justice", Title: "Genesis"}}

	for _, name := range []string{"weekend jams", "weekend jams.csv", "weekend jams.CSV", "mix.2026"} {
		// Each filename check owns its destination; headless exports no longer
		// silently overwrite a previous file with the same normalized name.
		api := New(newLoadedContainer(t), nil)
		res, err := api.ExportCSV(name, rows)
		if err != nil {
			t.Fatalf("ExportCSV(%q): %v", name, err)
		}
		base := filepath.Base(res.Path)
		if ext := filepath.Ext(base); ext != ".csv" && ext != ".CSV" {
			t.Errorf("ExportCSV(%q) -> %q: want a .csv extension", name, base)
		}
		if bytes.Count([]byte(base), []byte(".csv"))+bytes.Count([]byte(base), []byte(".CSV")) > 1 {
			t.Errorf("ExportCSV(%q) -> %q: extension doubled", name, base)
		}
	}
}

func TestPrepareExportWithoutEnrichment(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	c.Enrich = nil
	api := New(c, nil)
	ids := []string{"seed0003", "missing", "seed0001", "seed0003"}
	rows, err := api.PrepareExport(ids)
	if err != nil || len(rows) != 3 {
		t.Fatalf("local preparation failed: %v %v", rows, err)
	}
	for i, id := range []string{"seed0003", "seed0001", "seed0003"} {
		meta, _ := c.Runtime().Catalog.Meta(id)
		if rows[i] != (ExportTrackDTO{ID: id, Artist: meta.Ref.Artist, Title: meta.Ref.Title, Album: meta.Album}) {
			t.Fatalf("local metadata/order changed: %+v", rows[i])
		}
	}
	res, err := api.ExportCSV("Local playlist", rows)
	if err != nil || res.Count != 3 {
		t.Fatalf("export without enrichment failed: %+v %v", res, err)
	}
}

func TestPrepareExportResolvesLocalLibraryTrackAfterGenerationOverlayCloses(t *testing.T) {
	c := newLoadedContainer(t)
	id := importBridgeLibraryTrack(t, c)

	rows, err := New(c, nil).PrepareExport([]string{id})
	if err != nil || len(rows) != 1 {
		t.Fatalf("local library export preparation = %+v, %v", rows, err)
	}
	if rows[0] != (ExportTrackDTO{ID: id, Artist: "Local Artist", Title: "Gentle Pulse", Album: "Local Album"}) {
		t.Fatalf("local library export metadata changed: %+v", rows[0])
	}
}
