package libraryindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanSpillPreservesFilenameBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scan-staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, tx, path, err := newScanSpill(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer func() { _ = tx.Rollback() }()
	want := SourceFile{RootID: "root", RelativePath: "track-\xff.flac", Extension: ".flac"}
	if err := normalizeSourceFile(&want); err != nil {
		t.Fatal(err)
	}
	if err := writeScanSpill(ctx, tx, scanDirectoryChunk{Files: []SourceFile{want}, Children: []string{"directory-\xfe"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var files []SourceFile
	var children []string
	if err := readScanSpill(ctx, path, func(chunk scanDirectoryChunk) error {
		files = append(files, chunk.Files...)
		children = append(children, chunk.Children...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != want {
		t.Fatalf("spill altered file identity: got=%+v want=%+v", files, want)
	}
	if len(children) != 1 || children[0] != "directory-\xfe" {
		t.Fatalf("spill altered directory identity: %q", children)
	}
}
