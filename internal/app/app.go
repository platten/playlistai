package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/platten/playlistai/internal/config"
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
	return c, nil
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
