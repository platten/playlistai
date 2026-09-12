package nlu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCancelledSetupDoesNotCreateAssets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uncreated")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := InstallAssets(ctx, dir, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("canceled setup wrote to disk")
	}
}

func TestCorruptAssetNeverReportsReady(t *testing.T) {
	dir := t.TempDir()
	if AssetsReady(dir) {
		t.Fatal("empty setup accepted")
	}
	f := filepath.Join(dir, "sample")
	good := []byte("model-good")
	sum := sha256.Sum256(good)
	if err := os.WriteFile(f, []byte("model-evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	if verified(f, int64(len(good)), hex.EncodeToString(sum[:])) {
		t.Fatal("same-size corrupt file accepted")
	}
}

func TestPinnedSourcesHaveNoPathCollisions(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Sources() {
		if !s.Setup {
			continue
		}
		p := sourcePath("root", s)
		if seen[p] {
			t.Fatal("setup file collision", p)
		}
		seen[p] = true
		if s.Size <= 0 || len(s.SHA256) != 64 || s.License == "" {
			t.Fatal("unpinned source", s)
		}
	}
	if ModelSHA256(MiniLM) == "" || ModelSHA256(DistilBERT) == "" {
		t.Fatal("encoder missing")
	}
}
