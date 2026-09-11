package semantic

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestSearchExcludesBeforeTopKAndKeepsStableTies(t *testing.T) {
	var rows []sidecarRow
	for _, id := range []string{"a", "b", "c", "d"} {
		rows = append(rows, sidecarRow{id: id, vector: []float32{1, 0}, feature: core.TrackFeatures{SchemaVersion: core.CurrentFeatureSchemaVersion, CatalogVersion: "fake:v1", TrackID: id}})
	}
	store, err := Open(writeSidecar(t, rows), "fake:v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	excluded := map[string]struct{}{"a": {}, "b": {}}
	hits, err := store.Search(context.Background(), "relaxing", 1, excluded)
	if err != nil || len(hits) != 1 || hits[0].TrackID != "c" || len(excluded) != 2 {
		t.Fatalf("excluded prefix consumed top-K: %+v %v", hits, err)
	}
	excluded["c"], excluded["d"] = struct{}{}, struct{}{}
	hits, err = store.Search(context.Background(), "relaxing", 2, excluded)
	if err != nil || len(hits) != 0 {
		t.Fatalf("fully excluded index: %+v %v", hits, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Search(ctx, "relaxing", 1, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search: %v", err)
	}
}
