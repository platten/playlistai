package semantic

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func validSidecarRows() []sidecarRow {
	return []sidecarRow{{id: "one", vector: []float32{1, 0}, feature: core.TrackFeatures{SchemaVersion: core.CurrentFeatureSchemaVersion, CatalogVersion: "fake:v1", TrackID: "one"}}}
}

func mutateSidecar(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsIncompleteOrIncompatibleMetadata(t *testing.T) {
	for _, tc := range []struct{ name, mutation, want string }{
		{"missing-meta", "DROP TABLE meta", "metadata"},
		{"schema-text", "UPDATE meta SET value='bad' WHERE key='schema_version'", "schema_version"},
		{"dimension-text", "UPDATE meta SET value='bad' WHERE key='embedding_dim'", "embedding_dim"},
		{"count-text", "UPDATE meta SET value='bad' WHERE key='track_count'", "track_count"},
		{"missing-revision", "DELETE FROM meta WHERE key='model_revision'", "incomplete version"},
		{"bad-facets", "UPDATE meta SET value='not-json' WHERE key='supported_facets'", "supported facets"},
		{"missing-features", "DROP TABLE features", "features"},
		{"missing-vectors", "DROP TABLE semantic_vectors", "semantic_vectors"},
		{"wrong-count", "UPDATE meta SET value='2' WHERE key='track_count'", "declared 2 tracks"},
		{"wrong-encoder", "UPDATE meta SET value='different' WHERE key='query_encoder'", "unsupported query encoder"},
		{"wrong-query-count", "UPDATE meta SET value='0' WHERE key='query_term_count'", "invalid query_term_count"},
		{"missing-queries", "DROP TABLE query_vectors", "query vocabulary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSidecar(t, validSidecarRows())
			mutateSidecar(t, path, tc.mutation)
			store, err := Open(path, "fake:v1", nil)
			if store != nil {
				_ = store.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("bad sidecar was accepted: %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing.sqlite"), "fake:v1", nil); err == nil {
		t.Fatal("read-only open created a missing sidecar")
	}
}

func TestSemanticOpenPreservesReservedFilenameCharacters(t *testing.T) {
	path := writeSidecar(t, validSidecarRows())
	escaped := filepath.Join(filepath.Dir(path), "semantic # + % café.sqlite")
	if err := os.Rename(path, escaped); err != nil {
		t.Fatal(err)
	}
	store, err := Open(escaped, "fake:v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if info := store.Info(); info.TrackCount != 1 || info.CatalogVersion != "fake:v1" {
		t.Fatalf("wrong sidecar opened: %+v", info)
	}
	if _, err := store.db.Exec("DELETE FROM features"); err == nil {
		t.Fatal("escaped URI lost read-only mode")
	}
}

func TestFeatureRowsRejectMalformedAndMismatchedIdentity(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"{", "decode features"},
		{`{"schemaVersion":3,"catalogVersion":"other","trackId":"one"}`, "incompatible feature row"},
		{`{"schemaVersion":3,"catalogVersion":"fake:v1","trackId":"one","vocalEvidence":{"value":"words","missingness":"unknown"}}`, "unknown value contains text"},
	} {
		path := writeSidecar(t, validSidecarRows())
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("UPDATE features SET feature_json=?", tc.raw)
		_ = db.Close()
		if err != nil {
			t.Fatal(err)
		}
		store, err := Open(path, "fake:v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, ok, err := store.Features(context.Background(), "one")
		if err == nil || ok || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("bad feature was accepted: ok=%v err=%v want=%s", ok, err, tc.want)
		}
		if _, ok, err := store.Features(context.Background(), "missing"); err != nil || ok {
			t.Fatalf("missing feature invented: %v %v", ok, err)
		}
		_ = store.Close()
		if _, _, err := store.Features(context.Background(), "one"); err == nil {
			t.Fatal("closed feature store read succeeded")
		}
	}
}

func TestFeatureValidationPreservesKnownUnknownAndCoverageContract(t *testing.T) {
	known := core.FeatureValue{Value: "instrumental", Missingness: core.FeatureKnown, Confidence: .9, Provenance: []core.FeatureProvenance{{Source: "fixture"}}}
	for _, tc := range []struct {
		name    string
		feature core.TrackFeatures
		valid   bool
	}{
		{"known-vocals", core.TrackFeatures{VocalEvidence: known}, true},
		{"known-without-evidence", core.TrackFeatures{VocalEvidence: core.FeatureValue{Value: "vocal", Missingness: core.FeatureKnown}}, false},
		{"unknown-with-text", core.TrackFeatures{VocalEvidence: core.FeatureValue{Value: "vocal", Missingness: core.FeatureUnknown}}, false},
		{"invalid-missingness", core.TrackFeatures{VocalEvidence: core.FeatureValue{Missingness: "maybe"}}, false},
		{"legacy-coverage", core.TrackFeatures{SchemaVersion: 2, FacetCoverage: []string{"tags"}}, false},
		{"invalid-coverage", core.TrackFeatures{SchemaVersion: 3, FacetCoverage: []string{"tempo"}}, false},
		{"valid-coverage", core.TrackFeatures{SchemaVersion: 3, FacetCoverage: []string{"tags"}}, true},
		{"invalid-preview", core.TrackFeatures{Preview: core.PreviewCoverage{Available: true, StartSeconds: 2, EndSeconds: 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFeature(tc.feature); (err == nil) != tc.valid {
				t.Fatalf("feature validity = %v, wanted %v", err, tc.valid)
			}
		})
	}
	known.Value = "other"
	if err := validateFeature(core.TrackFeatures{VocalEvidence: known}); err == nil {
		t.Fatal("unsupported vocal value accepted")
	}
}

func TestSearchAndScoreReportCorruptVectorsInsteadOfMissingEvidence(t *testing.T) {
	for _, table := range []string{"semantic_vectors", "query_vectors"} {
		path := writeSidecar(t, validSidecarRows())
		mutateSidecar(t, path, "UPDATE "+table+" SET embedding=x'00'")
		store, err := Open(path, "fake:v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Search(context.Background(), "relaxing", 1, nil); err == nil {
			t.Fatal("corrupt vector returned search results")
		}
		if _, _, err := store.Score(context.Background(), "relaxing", []string{"one"}); err == nil {
			t.Fatal("corrupt vector returned evidence")
		}
		if table == "query_vectors" {
			if _, _, err := store.Score(context.Background(), "relaxing mood", nil); err == nil {
				t.Fatal("corrupt composed query vector accepted")
			}
		}
		_ = store.Close()
	}
	store, err := Open(writeSidecar(t, validSidecarRows()), "fake:v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, text := range []string{"", "and the", "outside-vocabulary"} {
		if _, _, err := store.Score(context.Background(), text, []string{"one"}); !errors.Is(err, core.ErrUnavailable) {
			t.Fatalf("unsearchable query %q: %v", text, err)
		}
	}
	if hits, err := store.Search(context.Background(), "", 1, nil); err != nil || len(hits) != 0 {
		t.Fatalf("empty search: %+v %v", hits, err)
	}
	store.queryReady = false
	if _, _, err := store.Score(context.Background(), "relaxing", nil); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("legacy scoring falsely available: %v", err)
	}
	zero := []float32{0, 0}
	normalize(zero)
	if !reflect.DeepEqual(zero, []float32{0, 0}) || len(queryKeys("and the")) != 0 || !reflect.DeepEqual(queryKeys("relaxing relaxing"), []string{"relaxing relaxing", "relaxing"}) {
		t.Fatal("zero vector or duplicate vocabulary normalization changed")
	}
}
