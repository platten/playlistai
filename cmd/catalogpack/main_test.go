package main

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/dataset"
)

func TestCatalogPackRoundTrip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.tar.zst")
	if err := run("../../internal/catalog/testdata", archive); err != nil {
		t.Fatal(err)
	}
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"catalogpack", "-in", "../../internal/catalog/testdata", "-out", archive}
	main()
	unpacked := filepath.Join(dir, "unpacked")
	if err := dataset.Unpack(context.Background(), archive, unpacked, nil); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(unpacked)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Len() != 256 {
		t.Fatal("catalog rows changed")
	}
	if _, err := os.Stat(archive + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary archive retained")
	}
}

func TestCatalogPackRejectsInvalidInputAndCleansTemporaryArchive(t *testing.T) {
	for name, raw := range map[string]string{"invalid": "{", "empty": `{"files":[]}`, "missing": `{"files":[{"name":"absent","size":1}]}`, "wrong size": `{"files":[{"name":"present","size":3}]}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "catalog-manifest.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "present"), []byte("ok"), 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "output.tar.zst")
			if run(dir, out) == nil {
				t.Fatal("accepted invalid input")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("published invalid archive")
			}
		})
	}
	if run(t.TempDir(), filepath.Join(t.TempDir(), "out")) == nil {
		t.Fatal("accepted missing manifest")
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := writeTarEntry(tw, "entry", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&b)
	header, err := reader.Next()
	if err != nil || header.Name != "entry" || header.Size != 4 {
		t.Fatalf("entry header: %+v %v", header, err)
	}
	if writeTarEntry(tw, "after-close", nil) == nil {
		t.Fatal("wrote closed archive")
	}
}
