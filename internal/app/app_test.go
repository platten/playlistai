package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
	if !c.Ready() {
		// Ready needs Sim + Reco too (later milestones); Catalog alone is fine here.
		if c.Catalog == nil {
			t.Fatal("catalog missing")
		}
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
