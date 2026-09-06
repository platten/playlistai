package semantic

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type fixedEncoder struct {
	name, revision string
	vector         []float32
}

func (e fixedEncoder) Model() (string, string, int) { return e.name, e.revision, len(e.vector) }
func (e fixedEncoder) Embed(context.Context, string) ([]float32, error) {
	return append([]float32(nil), e.vector...), nil
}

func TestStorePreservesGroundedFeaturesAndSkipsUnknownCatalogIDs(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "grounded", Display: "Artist - Grounded", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "other", Display: "Artist - Other", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
	path := writeSidecar(t, []sidecarRow{
		{id: "grounded", vector: []float32{1, 0}, feature: core.TrackFeatures{
			SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: "grounded",
			RecordingID:   core.FeatureValue{Value: "mb-recording", Missingness: core.FeatureKnown, Confidence: .98, Provenance: []core.FeatureProvenance{{Source: "musicbrainz", SourceID: "mb-recording", SourceVersion: "2026-01", Confidence: .98}}},
			VocalEvidence: core.FeatureValue{Missingness: core.FeatureUnknown},
			ReleaseDates: core.ReleaseDateFeatures{
				OriginalEdition: core.FeatureValue{Value: "1998", Missingness: core.FeatureKnown, Confidence: .9, Provenance: []core.FeatureProvenance{{Source: "label-discography", SourceID: "release-group", Confidence: .9}}},
				ReleaseEdition:  core.FeatureValue{Missingness: core.FeatureUnknown},
			},
			Preview: core.PreviewCoverage{Available: true, StartSeconds: 30, EndSeconds: 60, CoveredSeconds: 30, Source: "licensed-preview"},
		}},
		{id: "ghost", vector: []float32{.99, .01}, feature: core.TrackFeatures{SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: "ghost"}},
		{id: "other", vector: []float32{0, 1}, feature: core.TrackFeatures{SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: "other"}},
	})
	store, err := Open(path, "fake:v1", cat, fixedEncoder{name: "pilot-model", revision: "abc123", vector: []float32{1, 0}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	feature, ok, err := store.Features(context.Background(), "grounded")
	if err != nil || !ok {
		t.Fatalf("features: ok=%v err=%v", ok, err)
	}
	if feature.RecordingID.Value != "mb-recording" || feature.RecordingID.Provenance[0].Source != "musicbrainz" || feature.VocalEvidence.Missingness != core.FeatureUnknown {
		t.Fatalf("feature propagation lost evidence or unknown: %+v", feature)
	}
	if feature.ReleaseDates.OriginalEdition.Value != "1998" || feature.ReleaseDates.ReleaseEdition.Missingness != core.FeatureUnknown || feature.Preview.CoveredSeconds != 30 {
		t.Fatalf("release or segment coverage semantics drifted: %+v", feature)
	}
	hits, err := store.Search(context.Background(), "relaxing", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].TrackID != "grounded" {
		t.Fatalf("search returned ungrounded or misranked IDs: %+v", hits)
	}
}

func TestStoreRejectsCatalogAndModelMismatch(t *testing.T) {
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "A - One", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	path := writeSidecar(t, []sidecarRow{{id: "one", vector: []float32{1, 0}, feature: core.TrackFeatures{SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: "one"}}})
	if _, err := Open(path, "other-catalog", cat, nil); err == nil || !strings.Contains(err.Error(), "catalog version") {
		t.Fatalf("catalog mismatch = %v", err)
	}
	if _, err := Open(path, "fake:v1", cat, fixedEncoder{name: "wrong", revision: "abc123", vector: []float32{1, 0}}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("model mismatch = %v", err)
	}
	schemaPath := writeSidecar(t, []sidecarRow{{id: "one", vector: []float32{1, 0}, feature: core.TrackFeatures{SchemaVersion: 1, CatalogVersion: "fake:v1", TrackID: "one"}}})
	db, err := sql.Open("sqlite", schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE meta SET value='2' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(schemaPath, "fake:v1", cat, nil); err == nil || !strings.Contains(err.Error(), "schema 2") {
		t.Fatalf("schema mismatch = %v", err)
	}
}

type sidecarRow struct {
	id      string
	vector  []float32
	feature core.TrackFeatures
}

func writeSidecar(t *testing.T, rows []sidecarRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DBFile)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT NOT NULL); CREATE TABLE features(track_id TEXT PRIMARY KEY,feature_json TEXT NOT NULL); CREATE TABLE semantic_vectors(track_id TEXT PRIMARY KEY,embedding BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"schema_version": "1", "catalog_version": "fake:v1", "feature_version": "pilot/v1", "text_model": "pilot-model", "model_revision": "abc123", "embedding_dim": "2", "track_count": strconv.Itoa(len(rows)), "supported_facets": "[\"styles\",\"vocal_evidence\"]"}
	for key, value := range meta {
		if _, err := db.Exec("INSERT INTO meta VALUES(?,?)", key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range rows {
		raw, _ := json.Marshal(row.feature)
		blob := make([]byte, 4*len(row.vector))
		for i, value := range row.vector {
			binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(value))
		}
		if _, err := db.Exec("INSERT INTO features VALUES(?,?)", row.id, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO semantic_vectors VALUES(?,?)", row.id, blob); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
