package semantic

import (
	"context"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestConcurrentSearchKeepsQueryExclusionsIndependent(t *testing.T) {
	var rows []sidecarRow
	for _, id := range []string{"a", "b", "c", "d"} {
		rows = append(rows, sidecarRow{id: id, vector: []float32{1, 0}, feature: core.TrackFeatures{SchemaVersion: core.CurrentFeatureSchemaVersion, CatalogVersion: "fake:v1", TrackID: id}})
	}
	store, err := Open(writeSidecar(t, rows), "fake:v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.ConcurrentSearch() {
		t.Fatal("immutable store did not advertise concurrent reads")
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			excluded := map[string]struct{}{}
			if i%2 == 0 {
				excluded["a"] = struct{}{}
			}
			hits, err := store.Search(context.Background(), "relaxing", 2, excluded)
			want := "a"
			if i%2 == 0 {
				want = "b"
			}
			if err != nil || len(hits) != 2 || hits[0].TrackID != want {
				t.Errorf("query=%d hits=%v err=%v", i, hits, err)
			}
		}()
	}
	wg.Wait()
}
