package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
)

func TestResetAssetsPreservesPersonalDataAndRequiresSetup(t *testing.T) {
	cfg := testConfig(t)
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"models", "catalog", "metadata", "mert-analysis", "intent-nlu"} {
		path := filepath.Join(cfg.DataDir, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "asset"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	personal := filepath.Join(cfg.DataDir, "personal.txt")
	if err := os.WriteFile(personal, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (config.Prefs{OnboardingDone: true, ModelPath: "external.gguf"}).Save(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(cfg.DataDir, "legacy.gguf")
	if err := os.WriteFile(legacy, []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, release := c.OperationContext(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.ResetAssets() }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reset did not cancel active work")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("removed assets before work stopped", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy model retained", err)
	}
	for _, name := range []string{"models", "catalog", "metadata", "mert-analysis", "intent-nlu"} {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, name)); !os.IsNotExist(err) {
			t.Fatal(name, err)
		}
	}
	if _, err := os.Stat(personal); err != nil {
		t.Fatal(err)
	}
	if p := config.LoadPrefs(cfg.DataDir); p.OnboardingDone || p.ModelPath != "" {
		t.Fatal(p)
	}
	next, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Onboarded() {
		t.Fatal("setup remained completed")
	}
}
