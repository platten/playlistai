package bridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func newTestContainer(t *testing.T) *app.Container {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Catalog.Dir = filepath.Join(cfg.DataDir, "catalog")
	cfg.Catalog.ArchiveURL = "" // tests never hit the network for the catalog
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
	if st.ParserBackend != "rules" || !st.ParserReady {
		t.Fatalf("parser = %q ready=%v, want rules/true (always available)", st.ParserBackend, st.ParserReady)
	}
	if st.PreviewMode != config.PreviewDeezer {
		t.Fatalf("PreviewMode = %q, want deezer", st.PreviewMode)
	}
	if st.Version != Version {
		t.Fatalf("Version = %q, want %q", st.Version, Version)
	}
	if st.GenerateReady || st.LLMReady {
		t.Fatal("no model configured => GenerateReady and LLMReady must be false")
	}
}

func TestGetStatusEnablesGenerateWithRulesAndCatalog(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)

	st := api.GetStatus()
	if !st.CoreReady || !st.GenerateReady {
		t.Fatalf("rules + catalog status = %+v, want generation ready", st)
	}
	if st.ParserBackend != "rules" || st.LLMReady {
		t.Fatalf("status should remain catalog-only: %+v", st)
	}
}

func TestServiceLifecycleNoWailsApp(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)

	if api.ServiceName() == "" {
		t.Fatal("ServiceName empty")
	}
	if err := api.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	if err := api.ServiceShutdown(); err != nil {
		t.Fatalf("ServiceShutdown: %v", err)
	}
}

func TestWailsProgressNoAppIsNoop(t *testing.T) {
	t.Parallel()
	// application.Get() is nil when no Wails app is running; Report must not panic.
	NewWailsProgress().Report("catalog", 1, 2, "downloading")
}
