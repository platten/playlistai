package bridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func newTestContainer(t *testing.T) *app.Container {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
	cfg.Enrich.CachePath = filepath.Join(cfg.DataDir, "mb.sqlite")
	c, err := app.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestGetStatusOnBareContainer(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)

	st := api.GetStatus()
	if st.CoreReady {
		t.Fatal("no ports wired => CoreReady must be false")
	}
	if st.CatalogLoaded {
		t.Fatal("no catalog => CatalogLoaded must be false")
	}
	if st.ParserBackend != "none" {
		t.Fatalf("ParserBackend = %q, want none", st.ParserBackend)
	}
	if st.PreviewMode != config.PreviewDeezer {
		t.Fatalf("PreviewMode = %q, want deezer", st.PreviewMode)
	}
	if st.Version != Version {
		t.Fatalf("Version = %q, want %q", st.Version, Version)
	}
}

func TestWailsProgressNilSafe(t *testing.T) {
	t.Parallel()
	var w *WailsProgress
	w.Report("x", 1, 2, "note") // nil receiver must not panic

	var noCtx context.Context
	NewWailsProgress(noCtx).Report("x", 1, 2, "note") // nil context must not panic
}
