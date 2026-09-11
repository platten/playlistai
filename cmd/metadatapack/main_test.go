package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/metadata"
)

func args(t *testing.T, values ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"metadatapack"}, values...)
	t.Cleanup(func() { os.Args = old })
}

func TestLocalMetadataBuildInspectAndPackage(t *testing.T) {
	dir := t.TempDir()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, err := gz.Write([]byte(`<releases><release id="1"><title>Fixture</title><artists><artist><id>1</id><name>Justice</name></artist></artists><genres><genre>Electronic</genre></genres><tracklist><track><position>1</position><title>Genesis</title></track></tracklist></release></releases>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "releases.xml.gz")
	if err := os.WriteFile(input, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "metadata.sqlite")
	t.Run("build", func(t *testing.T) {
		args(t, "-catalog", "../../internal/catalog/testdata", "-output", out, "-releases", input, "-releases-sha256", fmt.Sprintf("%x", sha256.Sum256(compressed.Bytes())), "-date", "20260901", "-workers", "1")
		if err := run(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := metadata.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasGenre(context.Background(), "Electronic") {
		t.Fatal("missing imported genre")
	}
	_ = s.Close()
	t.Run("inspect", func(t *testing.T) { args(t, "-inspect", out, "-query", "Electronic"); main() })
	bundle := filepath.Join(dir, "bundle")
	t.Run("package", func(t *testing.T) {
		args(t, "-pack", out, "-bundle-dir", bundle)
		if err := run(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("verify", func(t *testing.T) {
		args(t, "-verify-bundle", bundle)
		if err := run(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMetadataCLIRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][]string{
		"unknown flag": {"-bogus"}, "workers": {"-workers", "33"}, "missing catalog": {"-catalog", "", "-releases", "local"}, "existing output": {"-output", existing, "-releases", "local"}, "missing catalog files": {"-catalog", dir, "-output", filepath.Join(dir, "out"), "-releases", "local"}, "invalid date": {"-catalog", "../../internal/catalog/testdata", "-output", filepath.Join(dir, "out"), "-releases", "local", "-date", "invalid"}, "list invalid year": {"-list", "-year", "1999"}, "download invalid year": {"-download-only", "-year", "1999"}, "pack needs directory": {"-pack", "local"}, "missing pack": {"-pack", "local", "-bundle-dir", filepath.Join(dir, "bundle")}, "missing inspect": {"-inspect", "local"}, "missing bundle": {"-verify-bundle", dir}, "missing compression input": {"-compression-benchmark", "local"},
	} {
		t.Run(name, func(t *testing.T) {
			args(t, values...)
			if run() == nil {
				t.Fatal("invalid operation accepted")
			}
		})
	}
	raw, err := os.ReadFile(existing)
	if err != nil || string(raw) != "preserve" {
		t.Fatal("existing output altered")
	}
}
