package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/export/soundiizcsv"
	"github.com/platten/playlistai/internal/export/soundiizhandoff"
	"github.com/platten/playlistai/internal/history"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/preview/spotifycdn"
	"github.com/platten/playlistai/internal/reco/deejai"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/semantic"
	"github.com/platten/playlistai/internal/similarity/brute"
	"github.com/platten/playlistai/internal/taste"
)

// Container holds the wired application. Fields are ports (interfaces); a field
// is nil until the milestone that provides its implementation lands. The bridge
// layer must tolerate nil ports and report "not ready" to the UI.
type Container struct {
	cfg config.Config
	log *slog.Logger

	Catalog      ports.Catalog
	Resolver     ports.ReferenceResolver
	Sim          ports.SimilarityEngine
	Reco         ports.RecommendationEngine
	BaselineReco ports.RecommendationEngine
	Enrich       ports.Enricher

	// History persists generated playlists for the Generate screen's
	// "start from a past playlist" option. nil if the DB could not be opened.
	History  *history.Store
	Feedback ports.FeedbackStore
	Profiles ports.ProfileStore
	Features ports.FeatureStore

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
			Runtimes:     c.LlamaRuntimes(),
			ModelPath:    modelPath,
			NCtx:         c.cfg.AI.NCtx,
			NThreads:     c.cfg.AI.NThreads,
			GPULayers:    c.cfg.AI.GPULayers,
			StartTimeout: runtimeStartTimeout,
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
	if fallbackErr != nil {
		return ParseOutcome{Backend: "rules", RequestedBackend: requested, FallbackUsed: true, FallbackReason: "parser_error"}, fallbackErr
	}
	return ParseOutcome{Intent: m, Backend: "rules", RequestedBackend: requested, FallbackUsed: true, FallbackReason: "parser_error"}, nil
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
	c.Resolver = cat
	c.RegisterCloser(cat.Close)
	c.Sim = brute.New(cat)
	c.BaselineReco = deejai.New(cat, c.Sim, cat)
	if c.cfg.Recommendation.Strategy == config.RecommendationDeejAI {
		c.Reco = c.BaselineReco
	} else {
		rc := c.cfg.Recommendation
		mc := multichannel.DefaultConfig()
		mc.SeedAudioBudget = rc.SeedAudioBudget
		mc.SeedCooccurrenceBudget = rc.SeedCooccurrenceBudget
		mc.TasteClusterBudget = rc.TasteClusterBudget
		mc.MaxTasteClusters = rc.MaxTasteClusters
		mc.ExplorationPool = rc.ExplorationPool
		mc.ExplorationBudget = rc.ExplorationBudget
		mc.ExplorationMinScore = rc.ExplorationMinScore
		mc.MaxCandidates = rc.MaxCandidates
		mc.RetrievalWeight = rc.RetrievalWeight
		mc.ListenerWeight = rc.ListenerWeight
		mc.NegativePenalty = rc.NegativePenalty
		mc.ExposurePenalty = rc.ExposurePenalty
		mc.NoveltyWeight = rc.NoveltyWeight
		mc.ExplorationChance = rc.ExplorationChance
		mc.ContinuationBudget = rc.ContinuationBudget
		mc.MMRMinimumLambda = rc.MMRMinimumLambda
		mc.SelectionMinimumRelevance = rc.SelectionMinimumRelevance
		mc.SelectionRelevanceWindow = rc.SelectionRelevanceWindow
		mc.EmbeddingRedundancyWeight = rc.EmbeddingRedundancyWeight
		mc.ArtistConcentrationWeight = rc.ArtistConcentrationWeight
		mc.AlbumConcentrationWeight = rc.AlbumConcentrationWeight
		mc.SoftArtistSpacingMax = rc.SoftArtistSpacingMax
		mc.TransitionRelevanceWeight = rc.TransitionRelevanceWeight
		mc.LocalImprovementPasses = rc.LocalImprovementPasses
		mc.LocalImprovementWindow = rc.LocalImprovementWindow
		mc.SemanticBudget = rc.SemanticBudget
		mc.SemanticMinimumScore = rc.SemanticMinimumScore
		mc.SemanticWeight = rc.SemanticWeight
		mc.SemanticNegativePenalty = rc.SemanticNegativePenalty
		var semanticSearch ports.SemanticSearcher
		if semanticCfg := c.cfg.Semantic; semanticCfg.SidecarPath != "" {
			var encoder ports.TextEmbedder
			if semanticCfg.ModelPath != "" {
				encoder = semantic.CommandEncoder{
					Python: semanticCfg.Python, Script: semanticCfg.QueryScript, ModelPath: semanticCfg.ModelPath,
					Name: semanticCfg.ModelName, Revision: semanticCfg.ModelRevision, Dimension: semanticCfg.EmbeddingDim,
				}
			}
			store, openErr := semantic.Open(semanticCfg.SidecarPath, cat.CatalogVersion(), cat, encoder)
			if openErr != nil {
				c.log.Warn("semantic sidecar unavailable; continuing without semantic matching", "err", openErr)
			} else {
				c.Features, semanticSearch = store, store
				c.RegisterCloser(store.Close)
				info := store.Info()
				c.log.Info("semantic sidecar loaded", "tracks", info.TrackCount, "feature_version", info.FeatureVersion, "model", info.TextModel)
			}
		}
		if semanticSearch != nil {
			c.Reco = multichannel.NewWithSemantic(cat, c.Sim, cat, c.Features, semanticSearch, mc)
		} else {
			c.Reco = multichannel.New(cat, c.Sim, cat, mc)
		}
	}
	c.log.Info("catalog loaded", "tracks", cat.Len(), "dim", cat.Dim())
	return nil
}

// EnsureCatalog gets the catalog onto disk and loads it, if it is not already
// present. In order:
//
//  1. a pre-packaged catalog.tar.zst staged next to the app (bundle_path or
//     beside the executable) — decompress it, no network;
//  2. cfg.Catalog.ArchiveURL — download the compressed archive (resumable,
//     checksummed), then decompress it;
//  3. cfg.Catalog.ManifestURL — download the two raw files.
//
// Progress is reported via p under the "catalog" op throughout, so the caller
// (the first-run gate) doesn't need to know which path ran.
func (c *Container) EnsureCatalog(ctx context.Context, p ports.Progress) error {
	if c.Catalog != nil {
		return nil
	}
	// Already unpacked on disk from a previous run? Load it — no download,
	// no decompress.
	if err := c.LoadCatalog(); err == nil {
		return nil
	}
	cat := c.cfg.Catalog

	if archive, ok := dataset.FindBundledArchive(cat.BundlePath); ok {
		if err := dataset.Unpack(ctx, archive, cat.Dir, p); err != nil {
			return fmt.Errorf("app: unpack bundled catalog: %w", err)
		}
		return c.LoadCatalog()
	}

	if cat.ArchiveURL != "" {
		archive := filepath.Join(c.cfg.DataDir, "catalog.tar.zst")
		if err := dataset.DownloadArchive(ctx, cat.ArchiveURL, archive, cat.ArchiveSize, cat.ArchiveSHA256, p); err != nil {
			return fmt.Errorf("app: download catalog: %w", err)
		}
		if err := dataset.Unpack(ctx, archive, cat.Dir, p); err != nil {
			return fmt.Errorf("app: unpack catalog: %w", err)
		}
		if err := c.LoadCatalog(); err != nil {
			return err
		}
		_ = os.Remove(archive) // decompressed copy is what we use from here
		return nil
	}

	if cat.ManifestURL == "" {
		return fmt.Errorf("app: no catalog source configured (set catalog.archive_url, catalog.manifest_url, or catalog.dir)")
	}
	m, err := dataset.LoadManifest(ctx, cat.ManifestURL)
	if err != nil {
		return err
	}
	if err := dataset.Fetch(ctx, cat.Dir, m, p); err != nil {
		return err
	}
	return c.LoadCatalog()
}

// CatalogBundled reports whether a pre-packaged, compressed catalog is staged
// next to the app and ready to be unpacked by EnsureCatalog — used by the
// bridge to tell the frontend whether "get the catalog" means an instant
// local decompression (auto-run, no user action) or a network download
// (user-initiated, per cfg.Catalog.ManifestURL).
func (c *Container) CatalogBundled() bool {
	_, ok := dataset.FindBundledArchive(c.cfg.Catalog.BundlePath)
	return ok
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
