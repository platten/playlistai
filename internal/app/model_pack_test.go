package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/modelpack"
)

func TestPrepareModelPackPreservesSourceAndCleansOnlyExtraction(t *testing.T) {
	t.Parallel()
	c := &Container{cfg: testConfig(t)}
	source := t.TempDir()
	path, cleanup, err := c.prepareModelPack(context.Background(), source, "mert-model", nil)
	if err != nil || path != source {
		t.Fatal(path, err)
	}
	cleanup()
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source removed", err)
	}
	var compressed bytes.Buffer
	z, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(z)
	data := []byte("fixture model")
	if err := tw.WriteHeader(&tar.Header{Name: "model.onnx", Mode: 0600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	part := compressed.Bytes()
	manifest := modelpack.Manifest{Version: 1, Name: "fixture", Parts: []modelpack.Part{{Path: "part001", Size: int64(len(part)), SHA256: fmt.Sprintf("%x", sha256.Sum256(part))}}, Files: []modelpack.File{{Path: "model.onnx", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	location := filepath.Join(source, "manifest.json")
	if err := os.WriteFile(location, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "part001"), part, 0600); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err = c.prepareModelPack(context.Background(), location, "mert-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(filepath.Join(path, "model.onnx")); err != nil || !bytes.Equal(actual, data) {
		t.Fatal("extracted bytes", err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary extraction remains", err)
	}
	if _, err := os.Stat(location); err != nil {
		t.Fatal("manifest removed", err)
	}
	// Verified cache survives a failed source download and is reused on retry.
	if err := os.Remove(filepath.Join(source, "part001")); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err = c.prepareModelPack(context.Background(), location, "mert-model", nil)
	if err != nil {
		t.Fatal("verified cache lost", err)
	}
	cleanup()
}
