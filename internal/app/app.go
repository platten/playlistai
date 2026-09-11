package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/export/soundiizcsv"
	"github.com/platten/playlistai/internal/export/soundiizhandoff"
	"github.com/platten/playlistai/internal/history"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/preview/spotifycdn"
	"github.com/platten/playlistai/internal/taste"
)

// Container owns application services and their lifetime. Optional stores may
// be nil after a recoverable startup failure; catalog-dependent services are
// published together by Runtime. The bridge reports those capabilities to UI.
type Container struct {
	catalogLoadMu     sync.Mutex
	metadataInstallMu sync.Mutex
	analysis          analysisState
	cfg               config.Config
	log               *slog.Logger

	Enrich    ports.Enricher
	Knowledge ports.MusicKnowledge

	// History persists generated playlists for the Generate screen's
	// "start from a past playlist" option. nil if the DB could not be opened.
	History  *history.Store
	Feedback ports.FeedbackStore
	Profiles ports.ProfileStore

	// exporters are the wired ports.Exporter implementations, looked up by
	// Name() via Exporter(). Order is display order.
	exporters []ports.Exporter

	// parser and preview can both be swapped at runtime (rules → llama once a
	// model is ready; preview provider from Settings/the first-run wizard), so
	// they sit behind accessor methods rather than bare fields. Every field
	// below the mutex is guarded by it.
	mu                 sync.Mutex
	runtime            RuntimeSnapshot
	closed             bool
	lifetime           context.Context
	stopLifetime       context.CancelFunc
	work               sync.WaitGroup
	closeDone          chan struct{}
	closeErr           error
	parser             ports.IntentParser
	rulesParser        ports.IntentParser
	llama              managedParser // active managed parser, if any
	modelRevision      uint64
	modelCancel        context.CancelFunc
	modelFactory       func(context.Context, llama.Options) (managedParser, error)
	modelDownloader    func(context.Context, modelmgr.Model, string, ports.Progress) (string, error)
	modelPath          string
	modelID            string
	preview            ports.PreviewProvider
	previewName        string
	recommendationMode core.RecommendationMode
	closers            []func() error
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
	prefs, prefsErr := config.LoadPrefsChecked(cfg.DataDir)
	if prefsErr != nil {
		log.Warn("preferences unavailable; original file preserved", "err", prefsErr)
	}
	if prefs.ModelPath != "" {
		cfg.AI.ModelPath = prefs.ModelPath
		cfg.AI.ModelID = prefs.ModelID
	}
	if isValidPreviewProvider(prefs.PreviewProvider) {
		cfg.Preview.Provider = prefs.PreviewProvider
	}

	c := &Container{cfg: cfg, log: log}
	c.lifetime, c.stopLifetime = context.WithCancel(ctx)
	c.recommendationMode = core.RecommendationMode(prefs.RecommendationMode)
	if hs, err := history.Open(cfg.DataDir); err != nil {
		log.Warn("playlist history unavailable; continuing without it", "err", err)
	} else {
		c.History = hs
		c.RegisterCloser(hs.Close)
	}
	if ts, err := taste.Open(cfg.DataDir); err != nil {
		log.Warn("taste data unavailable; continuing without personalization", "err", err)
	} else {
		c.Feedback = ts
		c.Profiles = ts
		c.RegisterCloser(ts.Close)
	}
	c.wireEnrichExport()
	c.wireAnalysis(ctx)
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
		AcousticBrainzURL: musicbrainz.AcousticBrainzURL,
		DatasetPath:       filepath.Join(c.cfg.DataDir, "metadata", "discogs.sqlite"),
		UserAgent:         c.cfg.Enrich.UserAgent,
		CachePath:         c.cfg.Enrich.CachePath,
		MirrorURL:         c.cfg.Enrich.MirrorURL,
		MinScore:          c.cfg.Enrich.MinScore,
		CredentialPath:    filepath.Join(c.cfg.DataDir, "credentials", "discogs-token"),
	})
	if err != nil {
		c.log.Warn("enricher unavailable; continuing without MusicBrainz", "err", err)
	} else {
		c.Enrich = mb
		c.Knowledge = mb
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
	p, name := previewFor(provider)
	c.mu.Lock()
	c.preview, c.previewName = p, name
	c.mu.Unlock()
}

func previewFor(provider string) (ports.PreviewProvider, string) {
	var p ports.PreviewProvider
	switch provider {
	case config.PreviewDeezer:
		p = deezer.New(deezer.Config{})
	case config.PreviewSpotify:
		p = spotifycdn.New()
	default:
		provider = config.PreviewOff
	}

	return p, provider
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
	c.mu.Lock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	prefs.PreviewProvider = provider
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("save preview provider: %w", err)
	}
	// Constructing these providers does not perform I/O. Publish the same
	// setting that was just persisted while still owning the settings lock.
	c.preview, c.previewName = previewFor(provider)
	c.mu.Unlock()
	c.log.Info("preview provider set", "provider", provider)
	return nil
}

// Onboarded reports whether the first-run wizard has been completed (or
// explicitly skipped).
func (c *Container) Onboarded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return config.LoadPrefs(c.cfg.DataDir).OnboardingDone
}

// SetOnboarded marks the first-run wizard done, persisting the flag.
func (c *Container) SetOnboarded() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.OnboardingDone = true
	return prefs.Save(c.cfg.DataDir)
}

// chooseParser installs the rules parser immediately, then — if a model is
// configured — spins up llama-server in the background and swaps it in once it
// is healthy. Startup is never blocked on the model.
func (c *Container) chooseParser(ctx context.Context) {
	r := rules.New()
	c.mu.Lock()
	c.rulesParser = r
	c.parser = r
	modelPath, modelID := c.cfg.AI.ModelPath, c.cfg.AI.ModelID
	c.mu.Unlock()

	if !c.cfg.LLMReady() {
		return
	}

	startCtx, revision, finish := c.beginModelChange(ctx)
	go func() {
		defer finish()
		p, err := c.startModel(startCtx, modelPath)
		if err == nil {
			err = c.commitModel(startCtx, revision, p, modelPath, modelID, false)
		}
		if err != nil && startCtx.Err() == nil {
			c.log.Warn("llama parser unavailable; staying on rules", "err", err)
		}
	}()
}

// IntentParser returns the active parser.
func (c *Container) IntentParser() ports.IntentParser {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parser
}

// ParseIntent parses in with the active parser, falling back to the
// always-available rules parser when the active one (llama) errors — a model
// timeout, a dead server, or unparseable output must never leave the user
// without an intent. When prog is non-nil and the model backend is active, it
// receives streaming progress under op "intent". Returns the intent and the
// backend that produced it ("llama" | "rules").
func (c *Container) ParseIntent(ctx context.Context, in ports.IntentInput, prog ports.Progress) (core.MusicIntent, string) {
	outcome, err := c.ParseIntentDetailed(ctx, in, prog)
	if err != nil {
		return core.MusicIntent{}, outcome.Backend
	}
	return outcome.Intent, outcome.Backend
}

type ParseOutcome struct {
	Intent           core.MusicIntent
	Backend          string
	RequestedBackend string
	FallbackUsed     bool
	FallbackReason   string
}

// ParseIntentDetailed preserves fallback and cancellation information for the
// generation lifecycle. Caller cancellation never triggers a fallback parse.
func (c *Container) ParseIntentDetailed(ctx context.Context, in ports.IntentInput, prog ports.Progress) (ParseOutcome, error) {
	c.mu.Lock()
	active, rp := c.parser, c.rulesParser
	c.mu.Unlock()
	requested := active.Info().Backend

	var m core.MusicIntent
	var err error
	if lp, ok := active.(*llama.Parser); ok {
		m, err = lp.ParseWithProgress(ctx, in, prog)
	} else {
		m, err = active.Parse(ctx, in)
	}
	if err == nil {
		return ParseOutcome{Intent: m, Backend: active.Info().Backend, RequestedBackend: requested}, nil
	}
	if ctx.Err() != nil {
		return ParseOutcome{Backend: requested, RequestedBackend: requested}, ctx.Err()
	}
	if active.Info().Backend == "rules" {
		return ParseOutcome{Intent: m, Backend: "rules", RequestedBackend: requested}, err
	}
	c.log.Warn("intent parser fallback", "from", requested, "to", "rules")

	if rp == nil {
		rp = rules.New()
	}
	m, fallbackErr := rp.Parse(ctx, in)
	reason := parserFallbackReason(err)
	if fallbackErr != nil {
		return ParseOutcome{Backend: "rules", RequestedBackend: requested, FallbackUsed: true, FallbackReason: reason}, fallbackErr
	}
	return ParseOutcome{Intent: m, Backend: "rules", RequestedBackend: requested, FallbackUsed: true, FallbackReason: reason}, nil
}

func parserFallbackReason(err error) string {
	var truncated *llama.TruncatedCompletionError
	if errors.As(err, &truncated) {
		return "truncated_output"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if strings.Contains(err.Error(), "schema:") {
		return "invalid_output"
	}
	return "parser_error"
}

// ParserIdentity identifies every input that can change parsing without
// retaining or logging the user's prompt.
func (c *Container) ParserIdentity() string {
	c.mu.Lock()
	parser, modelID, modelPath := c.parser, c.modelID, c.modelPath
	c.mu.Unlock()
	if parser == nil {
		return "none"
	}
	info := parser.Info()
	modelVersion := modelID
	if modelPath != "" {
		var size int64
		var modified int64
		if stat, err := os.Stat(modelPath); err == nil {
			size, modified = stat.Size(), stat.ModTime().UnixNano()
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", modelID, modelPath, size, modified)))
		modelVersion = fmt.Sprintf("%x", sum[:])
	}
	return fmt.Sprintf("%s|%s|%s|%d", info.Backend, info.Version, modelVersion, info.ContractVersion)
}

// SuggestTitle asks the active model for a short playlist name for prompt,
// bounded by timeout. It returns "" when the model backend is not active or
// produces nothing usable; the caller then derives a name locally.
func (c *Container) SuggestTitle(ctx context.Context, prompt string, timeout time.Duration) string {
	c.mu.Lock()
	lp, ok := c.parser.(*llama.Parser)
	c.mu.Unlock()
	if !ok {
		return ""
	}
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return lp.Title(tctx, prompt)
}

// Config returns the immutable configuration snapshot.
func (c *Container) Config() config.Config { return c.cfg }

// Logger returns the container logger.
func (c *Container) Logger() *slog.Logger { return c.log }

// Ready reports whether the core recommendation path (catalog + similarity +
// engine) is fully wired.
func (c *Container) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	snapshot := c.runtime
	return snapshot.Catalog != nil && snapshot.Sim != nil && snapshot.Reco != nil
}

// RegisterCloser adds a cleanup function to run on Close.
func (c *Container) RegisterCloser(fn func() error) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = fn()
		return
	}
	c.closers = append(c.closers, fn)
	c.mu.Unlock()
}

// Close stops the managed llama-server (if any) and releases every registered
// resource in reverse order of registration.
func (c *Container) Close() error {
	c.mu.Lock()
	if c.closeDone != nil {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		return c.closeErr
	}
	c.closed = true
	c.closeDone = make(chan struct{})
	if c.stopLifetime != nil {
		c.stopLifetime()
	}
	if c.modelCancel != nil {
		c.modelCancel()
	}
	c.mu.Unlock()
	// Cancellation precedes waiting: generation must release its runtime lease
	// before mapped vectors or model workers can be closed.
	c.work.Wait()
	c.catalogLoadMu.Lock()
	defer c.catalogLoadMu.Unlock()
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
	c.mu.Lock()
	c.closeErr = firstErr
	close(c.closeDone)
	c.mu.Unlock()
	return firstErr
}
