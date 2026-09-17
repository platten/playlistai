package librarysearch

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPinnedReaderSurvivesGenerationReplacement(t *testing.T) {
	root := t.TempDir()
	oldPath, oldManifest := buildFixture(t, root, "source-old", 2, false)
	newPath, newManifest := buildFixture(t, root, "source-new", 4, true)
	if oldManifest.Generation == newManifest.Generation {
		t.Fatal("distinct source generations collapsed")
	}
	manager, err := OpenManager(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Activate(context.Background(), oldPath); err != nil {
		t.Fatal(err)
	}
	old, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(context.Background(), newPath); err != nil {
		t.Fatal(err)
	}
	if retired := manager.CollectRetired(); len(retired) != 0 {
		t.Fatalf("pinned generation retired early: %v", retired)
	}
	oldHits, err := old.Search(context.Background(), Query{Vector: []float32{0, -1}, Limit: 1, Workers: 2})
	if err != nil || oldHits[0].ID == "f" {
		t.Fatalf("old handle mixed generations: %+v %v", oldHits, err)
	}
	fresh, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	freshHits, err := fresh.Search(context.Background(), Query{Vector: []float32{0, -1}, Limit: 1, Workers: 4})
	if err != nil || freshHits[0].ID != "f" {
		t.Fatalf("new generation unavailable: %+v %v", freshHits, err)
	}
	fresh.Release()
	old.Release()
	retired := manager.CollectRetired()
	if len(retired) != 1 || retired[0] != oldPath {
		t.Fatalf("old generation retirement=%v", retired)
	}
	reopened, err := OpenManager(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := reopened.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if handle.Manifest().Generation != newManifest.Generation {
		t.Fatal("active pointer did not survive reopen")
	}
	handle.Release()
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(newPath) != root {
		t.Fatal("fixture escaped manager root")
	}
}
