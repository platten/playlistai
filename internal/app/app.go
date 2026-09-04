package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// Container holds the wired application. Fields are ports (interfaces); a field
// is nil until the milestone that provides its implementation lands. The bridge
// layer must tolerate nil ports and report "not ready" to the UI.
type Container struct {
	cfg config.Config
	log *slog.Logger

	Catalog ports.Catalog
	Sim     ports.SimilarityEngine
	Reco    ports.RecommendationEngine
	Enrich  ports.Enricher
	Export  ports.Exporter
	Preview ports.PreviewProvider

	// parser can be swapped at runtime (rules → llama once a model is ready), so
	// it is behind IntentParser()/swapParser() rather than a bare field.
	mu      sync.Mutex
	parser  ports.IntentParser
	closers []func() error
}

// New validates config, ensures the data directory exists, wires the intent
// parser, and best-effort loads a catalog if one is present.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Container, error) {
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
	c.chooseParser(ctx)

	log.Info("container initialized",
		"data_dir", cfg.DataDir,
		"parser", c.IntentParser().Info().Backend,
		"llm_ready", cfg.LLMReady(),
		"preview", cfg.Preview.Provider,
	)

	if err := c.LoadCatalog(); err != nil {
		log.Info("catalog not loaded at startup", "dir", cfg.Catalog.Dir, "err", err)
	}

	return c, nil
}

// chooseParser installs the rules parser immediately, then — if a model is
// configured — spins up llama-server in the background and swaps it in once it
// is healthy. Startup is never blocked on the model.
func (c *Container) chooseParser(_ context.Context) {
	c.swapParser(rules.New())
	if !c.cfg.LLMReady() {
		return
	}

	go func() {
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		p, err := llama.New(startCtx, llama.Options{
			BinaryPath:   c.cfg.AI.LlamaServerPath,
			ModelPath:    c.cfg.AI.ModelPath,
			NCtx:         c.cfg.AI.NCtx,
			NThreads:     c.cfg.AI.NThreads,
			StartTimeout: 2 * time.Minute,
			Logger:       c.log,
		})
		if err != nil {
			c.log.Warn("llama parser unavailable; staying on rules", "err", err)
			return
		}
		c.RegisterCloser(p.Close)
		c.swapParser(p)
		c.log.Info("llama parser ready", "model", c.cfg.AI.ModelPath)
	}()
}

// IntentParser returns the active parser.
func (c *Container) IntentParser() ports.IntentParser {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parser
}

func (c *Container) swapParser(p ports.IntentParser) {
	c.mu.Lock()
	c.parser = p
	c.mu.Unlock()
}

// LoadCatalog opens the catalog in the configured directory and wires the
// similarity + recommendation engines. No-op if already loaded; returns an
// error without mutating the container if the directory has no valid catalog.
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
	c.Sim = brute.New(cat)
	c.Reco = deejai.New(cat, c.Sim)
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

// RegisterCloser adds a cleanup function to run on Close.
func (c *Container) RegisterCloser(fn func() error) {
	c.mu.Lock()
	c.closers = append(c.closers, fn)
	c.mu.Unlock()
}

// Close releases every registered resource in reverse order of registration.
func (c *Container) Close() error {
	c.mu.Lock()
	closers := c.closers
	c.closers = nil
	c.mu.Unlock()

	var firstErr error
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
