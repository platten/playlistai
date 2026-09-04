package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/export/soundiizcsv"
	"github.com/platten/playlistai/internal/export/soundiizhandoff"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/preview/spotifycdn"
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

	// exporters are the wired ports.Exporter implementations, looked up by
	// Name() via Exporter(). Order is display order.
	exporters []ports.Exporter

	// parser and preview can both be swapped at runtime (rules → llama once a
	// model is ready; preview provider from Settings/the first-run wizard), so
	// they sit behind accessor methods rather than bare fields. Every field
	// below the mutex is guarded by it.
	mu          sync.Mutex
	parser      ports.IntentParser
	rulesParser ports.IntentParser
	llama       *llama.Parser // active managed llama parser, if any
	modelPath   string
	modelID     string
	preview     ports.PreviewProvider
	previewName string
	closers     []func() error
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

	// Runtime prefs (from Settings or the first-run wizard) override the TOML
	// config.
	prefs := config.LoadPrefs(cfg.DataDir)
	if prefs.ModelPath != "" {
		cfg.AI.ModelPath = prefs.ModelPath
		cfg.AI.ModelID = prefs.ModelID
	}
	if isValidPreviewProvider(prefs.PreviewProvider) {
		cfg.Preview.Provider = prefs.PreviewProvider
	}

	c := &Container{cfg: cfg, log: log}
	c.wireEnrichExport()
	c.wirePreview(cfg.Preview.Provider)
	c.chooseParser(ctx)

	log.Info("container initialized",
		"data_dir", cfg.DataDir,
		"parser", c.IntentParser().Info().Backend,
		"llm_ready", cfg.LLMReady(),
		"preview", c.PreviewProviderName(),
	)

	if err := c.LoadCatalog(); err != nil {
		log.Info("catalog not loaded at startup", "dir", cfg.Catalog.Dir, "err", err)
	}

	return c, nil
}

// wireEnrichExport builds the MusicBrainz enricher and the two exporters. The
// enricher needs an on-disk cache; if it cannot be opened the app runs without
// enrichment (the review screen still works, just with no ISRC/metadata).
func (c *Container) wireEnrichExport() {
	mb, err := musicbrainz.New(musicbrainz.Config{
		UserAgent: c.cfg.Enrich.UserAgent,
		CachePath: c.cfg.Enrich.CachePath,
		MirrorURL: c.cfg.Enrich.MirrorURL,
		MinScore:  c.cfg.Enrich.MinScore,
	})
	if err != nil {
		c.log.Warn("enricher unavailable; continuing without MusicBrainz", "err", err)
	} else {
		c.Enrich = mb
		c.RegisterCloser(mb.Close)
	}

	c.exporters = []ports.Exporter{soundiizhandoff.New(), soundiizcsv.New()}
}

// Exporter returns the wired exporter with the given Name(), or false.
func (c *Container) Exporter(name string) (ports.Exporter, bool) {
	for _, e := range c.exporters {
		if e.Name() == name {
			return e, true
		}
	}
	return nil, false
}

// isValidPreviewProvider reports whether s is one of the recognized
// preview.provider values.
func isValidPreviewProvider(s string) bool {
	switch s {
	case config.PreviewDeezer, config.PreviewSpotify, config.PreviewOff:
		return true
	default:
		return false
	}
}

// wirePreview installs a preview backend by provider name. "deezer" queries
// the public Deezer search API (falling back to the catalog's bundled Spotify
// CDN URL on a miss); "spotify" uses only that bundled URL, no network; "off"
// (or anything unrecognized) leaves the provider nil and the UI disables
// playback.
func (c *Container) wirePreview(provider string) {
	var p ports.PreviewProvider
	switch provider {
	case config.PreviewDeezer:
		p = deezer.New(deezer.Config{})
	case config.PreviewSpotify:
		p = spotifycdn.New()
	default:
		provider = config.PreviewOff
	}

	c.mu.Lock()
	c.preview, c.previewName = p, provider
	c.mu.Unlock()
}

// PreviewProvider returns the active preview backend, or nil if previews are
// off.
func (c *Container) PreviewProvider() ports.PreviewProvider {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.preview
}

// PreviewProviderName returns the active provider's name ("deezer" | "spotify" | "off").
func (c *Container) PreviewProviderName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.previewName
}

// SetPreviewProvider switches the preview backend and persists the choice.
// Rejects anything other than "deezer", "spotify", or "off".
func (c *Container) SetPreviewProvider(provider string) error {
	if !isValidPreviewProvider(provider) {
		return fmt.Errorf("app: unknown preview provider %q", provider)
	}
	c.wirePreview(provider)

	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.PreviewProvider = provider
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		c.log.Warn("could not persist preview provider", "err", err)
	}
	c.log.Info("preview provider set", "provider", provider)
	return nil
}

// Onboarded reports whether the first-run wizard has been completed (or
// explicitly skipped).
func (c *Container) Onboarded() bool {
	return config.LoadPrefs(c.cfg.DataDir).OnboardingDone
}

// SetOnboarded marks the first-run wizard done, persisting the flag.
func (c *Container) SetOnboarded() error {
	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.OnboardingDone = true
	return prefs.Save(c.cfg.DataDir)
}

// chooseParser installs the rules parser immediately, then — if a model is
// configured — spins up llama-server in the background and swaps it in once it
// is healthy. Startup is never blocked on the model.
func (c *Container) chooseParser(_ context.Context) {
	r := rules.New()
	c.mu.Lock()
	c.rulesParser = r
	c.parser = r
	modelPath, modelID := c.cfg.AI.ModelPath, c.cfg.AI.ModelID
	c.mu.Unlock()

	if !c.cfg.LLMReady() {
		return
	}

	go func() {
		startCtx, cancel := context.WithTimeout(context.Background(), modelStartTimeout)
		defer cancel()

		p, err := llama.New(startCtx, llama.Options{
			BinaryPath:   c.cfg.AI.LlamaServerPath,
			ModelPath:    modelPath,
			NCtx:         c.cfg.AI.NCtx,
			NThreads:     c.cfg.AI.NThreads,
			StartTimeout: modelStartTimeout,
			Logger:       c.log,
		})
		if err != nil {
			c.log.Warn("llama parser unavailable; staying on rules", "err", err)
			return
		}
		c.setLlama(p, modelPath, modelID)
		c.log.Info("llama parser ready", "model", modelPath)
	}()
}

// IntentParser returns the active parser.
func (c *Container) IntentParser() ports.IntentParser {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parser
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

// Close stops the managed llama-server (if any) and releases every registered
// resource in reverse order of registration.
func (c *Container) Close() error {
	c.mu.Lock()
	closers := c.closers
	lm := c.llama
	c.closers, c.llama = nil, nil
	c.mu.Unlock()

	var firstErr error
	if lm != nil {
		if err := lm.Close(); err != nil {
			firstErr = err
		}
	}
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
