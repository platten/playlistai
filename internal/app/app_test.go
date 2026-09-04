package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "mb.sqlite")
	return cfg
}

func TestParserDefaultsToRules(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser backend = %q, want rules", got)
	}
}

func TestParserStaysRulesWhenLlamaBinaryMissing(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	model := filepath.Join(cfg.DataDir, "model.gguf")
	if err := os.WriteFile(model, []byte("not really a model but non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.AI.ModelPath = model
	cfg.AI.LlamaServerPath = filepath.Join(cfg.DataDir, "definitely-not-llama-server")

	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// The background attempt fails fast (binary not found); give it a moment.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.IntentParser().Info().Backend == "rules" {
			time.Sleep(200 * time.Millisecond) // ensure it didn't flip afterwards
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := c.IntentParser().Info().Backend; got != "rules" {
		t.Fatalf("parser backend = %q, want rules (llama binary is missing)", got)
	}
}

func TestNewCreatesDataDirAndIsNotReady(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Ready() {
		t.Fatal("skeleton container should not report Ready (no ports wired)")
	}
	if c.Config().DataDir == "" {
		t.Fatal("config not retained")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	bad := testConfig(t)
	bad.Preview.Provider = "napster"
	if _, err := New(context.Background(), bad, nil); err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestLoadCatalogFromFixture(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Catalog.Dir = filepath.Join("..", "catalog", "testdata")

	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Catalog == nil {
		t.Fatal("catalog should have loaded from the fixture dir")
	}
	if c.Catalog.Len() != 256 {
		t.Fatalf("catalog Len = %d, want 256", c.Catalog.Len())
	}
	if c.Sim == nil || c.Sim.Len() != 256 {
		t.Fatalf("similarity engine not wired to the loaded catalog")
	}
	if c.Reco == nil {
		t.Fatal("recommendation engine not wired to the loaded catalog")
	}
	if !c.Ready() {
		t.Fatal("Ready() should be true once catalog + sim + reco are wired")
	}
}

func TestEnsureCatalogWithoutManifest(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.EnsureCatalog(context.Background(), nil); err == nil {
		t.Fatal("EnsureCatalog should error when no manifest is configured")
	}
}

func TestCloseRunsClosersLIFOAndReturnsFirstError(t *testing.T) {
	t.Parallel()
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var order []int
	sentinel := errors.New("boom")
	c.RegisterCloser(func() error { order = append(order, 1); return nil })
	c.RegisterCloser(func() error { order = append(order, 2); return sentinel })
	c.RegisterCloser(func() error { order = append(order, 3); return nil })

	if err := c.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if len(order) != 3 || order[0] != 3 || order[2] != 1 {
		t.Fatalf("closers not LIFO: %v", order)
	}
}
