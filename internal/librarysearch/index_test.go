package librarysearch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func vectorRows(extra bool) []VectorRow {
	rows := []VectorRow{
		{ID: "a", Vector: []float32{1, 0}},
		{ID: "b", Vector: []float32{1, 0}},
		{ID: "c"},
		{ID: "d", Vector: []float32{0, 1}},
		{ID: "e", Vector: []float32{-1, 0}},
	}
	if extra {
		rows = append(rows, VectorRow{ID: "f", Vector: []float32{0, -1}})
	}
	return rows
}

func buildFixture(t *testing.T, root, source string, workers int, extra bool) (string, Manifest) {
	t.Helper()
	path, manifest, err := Build(context.Background(), &SliceSource{Rows: vectorRows(extra)}, BuildOptions{
		Root: root, SourceGeneration: source, Contract: "mert-fixture/v1",
		Dimension: 2, ShardRows: 2, Workers: workers,
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, manifest
}

func TestBuildAndExactSearchWorkerInvariant(t *testing.T) {
	root := t.TempDir()
	var generation string
	for _, workers := range []int{1, 2, 4} {
		path, manifest := buildFixture(t, root, "source-one", workers, false)
		if manifest.Rows != 4 || len(manifest.Shards) != 2 {
			t.Fatalf("manifest = %+v", manifest)
		}
		if generation != "" && manifest.Generation != generation {
			t.Fatal("physical build workers changed generation identity")
		}
		generation = manifest.Generation
		index, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, searchWorkers := range []int{1, 2, 4} {
			hits, err := index.Search(context.Background(), Query{Vector: []float32{4, 0}, Limit: 4, Workers: searchWorkers})
			if err != nil {
				t.Fatal(err)
			}
			want := []Hit{{ID: "a", Score: 1}, {ID: "b", Score: 1}, {ID: "d", Score: 0}, {ID: "e", Score: -1}}
			if !reflect.DeepEqual(hits, want) {
				t.Fatalf("workers=%d hits=%+v", searchWorkers, hits)
			}
		}
		excluded, err := index.Search(context.Background(), Query{Vector: []float32{1, 0}, Limit: 3, Workers: 4, Exclude: map[string]struct{}{"a": {}}})
		if err != nil || len(excluded) != 3 || excluded[0].ID != "b" {
			t.Fatalf("exclusion: %+v %v", excluded, err)
		}
		if err := index.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildAndSearchCancellationAndValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Build(ctx, &SliceSource{Rows: vectorRows(false)}, BuildOptions{Root: t.TempDir(), SourceGeneration: "s", Contract: "c", Dimension: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("build cancellation=%v", err)
	}
	bad := vectorRows(false)
	bad[1].ID = "a"
	if _, _, err := Build(context.Background(), &SliceSource{Rows: bad}, BuildOptions{Root: t.TempDir(), SourceGeneration: "s", Contract: "c", Dimension: 2}); err == nil {
		t.Fatal("duplicate source ID accepted")
	}
	path, _ := buildFixture(t, t.TempDir(), "source", 2, false)
	index, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := index.Search(ctx, Query{Vector: []float32{1, 0}, Limit: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("search cancellation=%v", err)
	}
	if _, err := index.Search(context.Background(), Query{Vector: []float32{0, 0}, Limit: 2}); err == nil {
		t.Fatal("zero query accepted")
	}
}

func TestOpenRejectsCorruptShard(t *testing.T) {
	path, manifest := buildFixture(t, t.TempDir(), "source", 2, false)
	vectorPath := filepath.Join(path, manifest.Shards[0].Vectors.Name)
	file, err := os.OpenFile(vectorPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0xff}, headerBytes); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); err == nil {
		t.Fatal("checksum-corrupt shard opened")
	}
}
