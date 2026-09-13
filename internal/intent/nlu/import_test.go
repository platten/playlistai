package nlu

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestImportSourcesPinsModelsBeforeChangingInstallation(t *testing.T) {
	t.Parallel()
	root, source := t.TempDir(), t.TempDir()
	data := []byte("pinned encoder")
	s := Source{Model: "distilbert", Name: "model.onnx", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Setup: true}
	write := func(path string, value []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(sourcePath(source, s), data)
	target := sourcePath(AssetDir(root), s)
	if err := importSources(context.Background(), root, source, []Source{s}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportSource(context.Background(), target, s); err != nil {
		t.Fatal(err)
	}
	write(sourcePath(source, s), []byte("wrong encoder!"))
	if err := importSources(context.Background(), root, source, []Source{s}); err == nil {
		t.Fatal("untrusted replacement accepted")
	}
	if err := checkImportSource(context.Background(), target, s); err != nil {
		t.Fatal("installed encoder changed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := importSources(ctx, root, source, []Source{s}); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestImportSourcesRequiresCompletePack(t *testing.T) {
	t.Parallel()
	root, source := t.TempDir(), t.TempDir()
	one := Source{Model: "minilm", Name: "model.onnx", Size: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("a"))), Setup: true}
	two := one
	two.Model = "distilbert"
	if err := os.MkdirAll(filepath.Join(source, "minilm"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath(source, one), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := importSources(context.Background(), root, source, []Source{one, two}); err == nil {
		t.Fatal("incomplete pack accepted")
	}
	if _, err := os.Stat(AssetDir(root)); !os.IsNotExist(err) {
		t.Fatal("partial pack changed installation", err)
	}
}
