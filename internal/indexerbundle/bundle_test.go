package indexerbundle

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppendedBundle(t *testing.T) {
	var payload bytes.Buffer
	zw := zip.NewWriter(&payload)
	w, err := zw.Create("codec/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "{}\n"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "indexer")
	raw := append([]byte("launcher"), payload.Bytes()...)
	raw = append(raw, Trailer(uint64(payload.Len()))...)
	if err := os.WriteFile(path, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := OpenExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	sub, err := bundle.Sub("codec")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(sub, "manifest.json")
	if err != nil || string(got) != "{}\n" {
		t.Fatalf("read=%q err=%v", got, err)
	}
}

func TestUnbundled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenExecutable(path)
	if !errors.Is(err, ErrNotBundled) {
		t.Fatalf("got %v", err)
	}
}
