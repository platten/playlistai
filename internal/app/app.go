package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

// Container holds the wired application. Fields are ports (interfaces); a field
// is nil until the milestone that provides its implementation lands. The bridge
// layer must tolerate nil ports and report "not ready" to the UI.
type Container struct {
	cfg config.Config
	log *slog.Logger

	Parser  ports.IntentParser
	Catalog ports.Catalog
	Sim     ports.SimilarityEngine
	Reco    ports.RecommendationEngine
	Enrich  ports.Enricher
	Export  ports.Exporter
	Preview ports.PreviewProvider

	closers []func() error
}

// New validates config, ensures the data directory exists, and returns a
// Container. Concrete ports are wired in later milestones; for now the
// Container is deliberately empty apart from config and logging.
func New(_ context.Context, cfg config.Config, log *slog.Logger) (*Container, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("app: invalid config: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("app: create data dir %s: %w", cfg.DataDir, err)
	}

	c := &Container{cfg: cfg, log: log}
	log.Info("container initialized",
		"data_dir", cfg.DataDir,
		"llm_ready", cfg.LLMReady(),
		"preview", cfg.Preview.Provider,
	)

	// Best-effort: if a catalog is already present, load it now.
	if err := c.LoadCatalog(); err != nil {
		log.Info("catalog not loaded at startup", "dir", cfg.Catalog.Dir, "err", err)
	}

	return c, nil
}

// LoadCatalog opens the catalog in the configured directory and wires it as the
// Catalog port. It is a no-op if a catalog is already loaded, and returns an
// error (without mutating the container) if the directory has no valid catalog.
func (c *Container) LoadCatalog() error {
	if c.Catalog != nil {
		return nil
	}
	cat, err := catalog.Open(c.cfg.Catalog.Dir)
	if err != nil {
		return err
	}
	c.Catalog = cat
	c.RegisterCloser(cat.Close)
	c.log.Info("catalog loaded", "tracks", cat.Len(), "dim", cat.Dim())
	return nil
}

// EnsureCatalog downloads the catalog (per cfg.Catalog.ManifestURL) if it is not
// already present, then loads it. Progress is reported via p.
func (c *Container) EnsureCatalog(ctx context.Context, p ports.Progress) error {
	if c.Catalog != nil {
		return nil
	}
	if c.cfg.Catalog.ManifestURL == "" {
		return fmt.Errorf("app: no catalog configured (set catalog.manifest_url or catalog.dir)")
	}
	m, err := dataset.LoadManifest(ctx, c.cfg.Catalog.ManifestURL)
	if err != nil {
		return err
	}
	if err := dataset.Fetch(ctx, c.cfg.Catalog.Dir, m, p); err != nil {
		return err
	}
	return c.LoadCatalog()
}

// Config returns the immutable configuration snapshot.
func (c *Container) Config() config.Config { return c.cfg }

// Logger returns the container logger.
func (c *Container) Logger() *slog.Logger { return c.log }

// Ready reports whether the core recommendation path (catalog + similarity +
// engine) is fully wired.
func (c *Container) Ready() bool {
	return c.Catalog != nil && c.Sim != nil && c.Reco != nil
}

// RegisterCloser adds a cleanup function to run on Close. Wiring code in later
// milestones uses it to tear down the SQLite handle, the llama subprocess, etc.
func (c *Container) RegisterCloser(fn func() error) { c.closers = append(c.closers, fn) }

// Close releases every registered resource in reverse order of registration.
func (c *Container) Close() error {
	var firstErr error
	for i := len(c.closers) - 1; i >= 0; i-- {
		if err := c.closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	c.closers = nil
	return firstErr
}
