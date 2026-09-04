package bridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func TestGetCatalogInfoUnconfigured(t *testing.T) {
	t.Parallel()
	// newTestContainer leaves catalog.manifest_url unset and points dir at a
	// path with nothing in it — the state of a fresh install today, since
	// this project doesn't ship or host a catalog (see docs/CATALOG.md).
	api := New(newTestContainer(t), nil)

	info := api.GetCatalogInfo()
	if info.Loaded {
		t.Fatal("nothing should be loaded")
	}
	if info.Configured {
		t.Fatal("no manifest_url set => Configured must be false")
	}
}

func TestGetCatalogInfoConfiguredButNotLoaded(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog") // empty, nothing to load
	cfg.Catalog.ManifestURL = "https://example.invalid/catalog-manifest.json"
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "mb.sqlite")

	c, err := app.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	api := New(c, nil)

	info := api.GetCatalogInfo()
	if info.Loaded {
		t.Fatal("nothing downloaded yet => Loaded must be false")
	}
	if !info.Configured {
		t.Fatal("manifest_url is set => Configured must be true")
	}
}

func TestGetCatalogInfoLoadedImpliesConfigured(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)

	info := api.GetCatalogInfo()
	if !info.Loaded || !info.Configured || info.TrackCount == 0 {
		t.Fatalf("loaded fixture catalog: %+v", info)
	}
}
