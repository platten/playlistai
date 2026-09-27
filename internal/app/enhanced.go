package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/modelpack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/taste"
)

const EnhancedAnalysisLimit = audio.EnhancedTrackLimit

type enhancedState struct {
	mu          sync.Mutex
	opMu        sync.Mutex
	startup     audioStartup
	enabled     bool
	mertEnabled bool
	bundles     *audio.MERTBundleManager
	alternate   *audio.MERTBundleManager
	worker      *audio.MERTWorker
	pool        *audio.MERTWorkerPool
	manifest    *audio.MERTBundleManifest
	healthCheck func(context.Context, *audio.MERTWorker) error // test seam
	detail      string
}

func (e *enhancedState) lockOperation(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.opMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type EnhancedAnalysisStatus struct {
	Loading                  bool                                 `json:"loading"`
	InstalledBackends        []string                             `json:"installedBackends"`
	RecommendedManifestURL   string                               `json:"recommendedManifestUrl"`
	RecommendedDownloadBytes int64                                `json:"recommendedDownloadBytes"`
	RecommendedUpgrade       bool                                 `json:"recommendedUpgrade"`
	UnsupportedReason        string                               `json:"unsupportedReason,omitempty"`
	Enabled                  bool                                 `json:"enabled"`
	MERTEnabled              bool                                 `json:"mertEnabled"`
	DSPAvailable             bool                                 `json:"dspAvailable"`
	MERTAvailable            bool                                 `json:"mertAvailable"`
	Installed                bool                                 `json:"installed"`
	Model                    string                               `json:"model"`
	Revision                 string                               `json:"revision"`
	License                  string                               `json:"license"`
	DownloadBytes            int64                                `json:"downloadBytes"`
	DSPStorage               core.DSPStorageUsage                 `json:"dspStorage"`
	MERTStorage              core.AudioRepresentationStorageUsage `json:"mertStorage"`
	SearchableTracks         int64                                `json:"searchableTracks"`
	Detail                   string                               `json:"detail"`
	Limit                    int                                  `json:"limit"`
}

type EnhancedAnalysisReport struct {
	Requested   int   `json:"requested"`
	Analyzed    int   `json:"analyzed"`
	Unavailable int   `json:"unavailable"`
	Bytes       int64 `json:"bytes"`
}

func (c *Container) wireEnhanced(ctx context.Context) {
	e := &c.enhanced
	// DSP preview measurements and MERT similarity are part of Enhanced hybrid.
	// They no longer have user-facing opt-outs, including for preferences saved
	// by older releases where either feature was disabled.
	e.enabled = true
	e.mertEnabled = true
	e.bundles = &audio.MERTBundleManager{Directory: filepath.Join(c.cfg.DataDir, "mert-analysis")}
	e.alternate = &audio.MERTBundleManager{Directory: filepath.Join(c.cfg.DataDir, "mert-analysis-alternate")}
	e.detail = "MERT finds similar tracks in Enhanced hybrid using available preview embeddings. DSP measurements are enabled automatically."
	if c.analysis.store == nil {
		return
	}
	c.RegisterCloser(func() error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.pool != nil {
			return e.pool.Close()
		}
		if e.worker != nil {
			return e.worker.Close()
		}
		return nil
	})
	if e.hasInstalledBundle() {
		e.startup.start(c, ctx, func(ctx context.Context) {
			e.opMu.Lock()
			defer e.opMu.Unlock()
			if err := c.loadMERT(ctx); err != nil && ctx.Err() == nil {
				e.mu.Lock()
				e.detail = "Installed MERT failed its health check. DSP remains available."
				e.mu.Unlock()
			}
		})
	}
}

func (c *Container) loadMERT(ctx context.Context) error {
	e := &c.enhanced
	started := time.Now()
	var manifest audio.MERTBundleManifest
	var worker *audio.MERTWorker
	var err error
	candidates := e.installedBundles(ctx, audio.MERTCUDAHostAvailable())
	verifiedLayout := time.Since(started)
	for _, candidate := range candidates {
		manifest = candidate.manifest
		worker = &audio.MERTWorker{BundleDir: candidate.dir, Model: manifest.Model}
		healthTimeout := 90 * time.Second
		if manifest.Backend() == "cuda" {
			healthTimeout = audio.MERTCUDAHealthTimeout
		}
		healthCtx, cancel := context.WithTimeout(ctx, healthTimeout)
		if e.healthCheck != nil {
			err = e.healthCheck(healthCtx, worker)
		} else {
			err = worker.Health(healthCtx)
		}
		cancel()
		if err == nil {
			break
		}
		_ = worker.Close()
		worker = nil
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Info("MERT startup check failed", "backend", manifest.Backend(), "health", time.Since(started))
	}
	if worker == nil {
		if err == nil {
			err = fmt.Errorf("no installed MERT bundle is available")
		}
		return err
	}
	healthDuration := time.Since(started) - verifiedLayout
	pool := audio.NewMERTWorkerPool(worker, audio.AnalysisParallelism())
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = pool.Close()
		return err
	}
	if e.pool != nil {
		_ = e.pool.Close()
	} else if e.worker != nil {
		_ = e.worker.Close()
	}
	e.worker, e.pool, e.manifest = worker, pool, &manifest
	e.detail = "MERT compares audio with audio, not with text. Preview measurements describe only the analyzed interval."
	if !e.mertEnabled {
		pool.Unload()
	}
	c.log.Info("MERT startup check complete", "layout", verifiedLayout, "health", healthDuration)
	return nil
}

func (c *Container) GetEnhancedAnalysisStatus(ctx context.Context) (EnhancedAnalysisStatus, error) {
	e := &c.enhanced
	e.mu.Lock()
	defer e.mu.Unlock()
	s := EnhancedAnalysisStatus{Enabled: e.enabled, MERTEnabled: e.mertEnabled, DSPAvailable: c.analysis.store != nil, Installed: e.manifest != nil, InstalledBackends: []string{}, MERTAvailable: e.worker != nil && audio.NativeInferenceAvailable(), Model: "MERT-v1-95M", License: "CC-BY-NC-4.0 (noncommercial)", Detail: e.detail, Limit: EnhancedAnalysisLimit}
	for _, candidate := range e.installedBundles(ctx, true) {
		s.InstalledBackends = append(s.InstalledBackends, candidate.manifest.Backend())
	}
	s.Loading = e.startup.loading()
	if distribution, err := c.recommendedMERT(); err != nil {
		s.UnsupportedReason = err.Error()
	} else {
		s.RecommendedManifestURL = distribution.URL
		s.RecommendedDownloadBytes = distribution.DownloadBytes
		if e.manifest != nil {
			if filepath.IsAbs(distribution.URL) {
				if recommended, readErr := audio.ReadStartupMERTBundleContext(ctx, distribution.URL); readErr == nil {
					s.RecommendedUpgrade = e.manifest.Model != recommended.Model
				}
			} else if strings.HasSuffix(distribution.Name, "-gpu") {
				s.RecommendedUpgrade = e.manifest.Backend() != "cuda"
			}
		}
	}
	if e.manifest != nil {
		s.Model = e.manifest.Label
		s.Revision = e.manifest.Model.Revision
		for _, a := range e.manifest.Artifacts {
			s.DownloadBytes += a.Size
		}
	}
	if c.analysis.store != nil {
		var err error
		s.DSPStorage, err = c.analysis.store.DSP().Usage(ctx)
		if err != nil {
			return s, err
		}
		s.MERTStorage, err = c.analysis.store.Representations().Usage(ctx)
		if err == nil && e.manifest != nil {
			rt := c.Runtime()
			if rt.Resolver != nil {
				coverage, searchErr := c.analysis.store.Representations().Search(ctx, ports.AudioRepresentationQuery{CatalogVersion: rt.Resolver.CatalogVersion(), Model: e.manifest.Model})
				if searchErr != nil {
					return s, searchErr
				}
				s.SearchableTracks = coverage.SearchableTracks
			}
		}
		return s, err
	}
	return s, nil
}

func (c *Container) SetEnhancedAnalysisEnabled(enabled bool) error {
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if enabled && c.analysis.store == nil {
		return fmt.Errorf("enhanced analysis storage unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	// Resolve the legacy shared opt-in before changing only the DSP setting.
	mertEnabled := prefs.MERTSimilarityEnabledValue()
	prefs.MERTSimilarityEnabled = &mertEnabled
	prefs.EnhancedAudioEnabled = enabled
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	e.enabled = enabled
	return nil
}

func (c *Container) SetMERTSimilarityEnabled(enabled bool) error {
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if enabled && c.analysis.store == nil {
		return fmt.Errorf("MERT similarity storage unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.MERTSimilarityEnabled = &enabled
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	e.mertEnabled = enabled
	if !enabled && e.pool != nil {
		e.pool.Unload()
	} else if !enabled && e.worker != nil {
		e.worker.Unload()
	}
	return nil
}

// InstallMERT imports a maintainer-prepared pack with verified hashes. The pack
// includes the native runtime for this OS/architecture and never requires Python.
func (c *Container) InstallMERT(ctx context.Context, directory string, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	e := &c.enhanced
	e.startup.stop()
	e.opMu.Lock()
	defer e.opMu.Unlock()
	return c.installMERTLocked(ctx, directory, p)
}

func (c *Container) installMERTLocked(ctx context.Context, directory string, p ports.Progress) error {
	e := &c.enhanced
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.analysis.store == nil || e.bundles == nil {
		return fmt.Errorf("enhanced analysis storage unavailable")
	}
	directory, cleanup, err := c.prepareModelPack(ctx, directory, "mert-model", p)
	if err != nil {
		return err
	}
	defer cleanup()
	manifest, err := audio.ReadMERTBundleContext(ctx, directory)
	if err != nil {
		return err
	}
	manager, err := e.managerFor(ctx, manifest.Backend())
	if err != nil {
		return err
	}
	if _, err := manager.InstallLocal(ctx, directory, p); err != nil {
		return err
	}
	return c.loadMERT(ctx)
}

func (c *Container) recommendedMERT() (modelpack.Distribution, error) {
	return c.recommendedMERTFor(audio.MERTCUDAHostAvailable())
}

func (c *Container) recommendedMERTFor(cudaAvailable bool) (modelpack.Distribution, error) {
	if !audio.NativeInferenceAvailable() {
		return modelpack.Distribution{}, fmt.Errorf("MERT requires a build with native inference support")
	}
	if c.analysis.store == nil || c.enhanced.bundles == nil {
		return modelpack.Distribution{}, fmt.Errorf("enhanced analysis storage unavailable")
	}
	if cudaAvailable {
		if dir, manifest, ok := preferredCUDAMERT(); ok {
			return modelpack.Distribution{Name: "mert-cuda-local", URL: dir, DownloadBytes: manifest.DownloadBytes()}, nil
		}
		if distribution, err := modelpack.RecommendedMERTGPU(runtime.GOOS, runtime.GOARCH); err == nil {
			return distribution, nil
		}
	}
	return modelpack.RecommendedMERT(runtime.GOOS, runtime.GOARCH)
}

// InstallRecommendedMERT downloads the pinned bundle for this native platform.
// Local import retains the same parity check and activation policy.
func (c *Container) InstallRecommendedMERT(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.enhanced.startup.stop()
	c.enhanced.opMu.Lock()
	defer c.enhanced.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := c.recommendedMERT()
	if err != nil {
		return err
	}
	return c.installMERTDistribution(ctx, p, d)
}

// InstallCPUMERT retains a CPU fallback even when CUDA is recommended.
func (c *Container) InstallCPUMERT(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.enhanced.startup.stop()
	c.enhanced.opMu.Lock()
	defer c.enhanced.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.analysis.store == nil || c.enhanced.bundles == nil {
		return fmt.Errorf("enhanced analysis storage unavailable")
	}
	d, err := modelpack.RecommendedMERT(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	return c.installMERTDistribution(ctx, p, d)
}

func (c *Container) installMERTDistribution(ctx context.Context, p ports.Progress, d modelpack.Distribution) error {
	var dir string
	var cleanup func()
	var err error
	if filepath.IsAbs(d.URL) {
		dir, cleanup, err = c.prepareModelPack(ctx, d.URL, "mert-model", p)
	} else {
		dir, cleanup, err = c.prepareRecommendedModelPack(ctx, d, "mert-model", p)
	}
	if err != nil {
		return err
	}
	defer cleanup()
	return c.installMERTLocked(ctx, dir, p)
}

func (c *Container) RemoveMERT() error {
	e := &c.enhanced
	e.startup.stop()
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pool != nil {
		_ = e.pool.Close()
	} else if e.worker != nil {
		_ = e.worker.Close()
	}
	e.worker = nil
	e.pool = nil
	e.manifest = nil
	if e.bundles == nil {
		return nil
	}
	if e.alternate == nil {
		return e.bundles.Remove()
	}
	return errors.Join(e.bundles.Remove(), e.alternate.Remove())
}

func (c *Container) ClearEnhancedAnalysis(ctx context.Context) error {
	c.enhanced.opMu.Lock()
	defer c.enhanced.opMu.Unlock()
	if c.analysis.store == nil {
		return nil
	}
	if err := c.analysis.store.DSP().Clear(ctx); err != nil {
		return err
	}
	return c.analysis.store.Representations().Clear(ctx)
}

func (c *Container) ClearMERTSimilarityCache(ctx context.Context) error {
	c.enhanced.opMu.Lock()
	defer c.enhanced.opMu.Unlock()
	if c.analysis.store == nil {
		return nil
	}
	return c.analysis.store.Representations().Clear(ctx)
}

func (c *Container) ClearDSPAnalysisCache(ctx context.Context) error {
	c.enhanced.opMu.Lock()
	defer c.enhanced.opMu.Unlock()
	if c.analysis.store == nil {
		return nil
	}
	return c.analysis.store.DSP().Clear(ctx)
}

func (c *Container) enhancedServices() (*audio.Service, *audio.MERTService) {
	recordings := c.previewRecordings()
	p := &audio.Service{Resolver: deezer.New(deezer.Config{}), Recordings: recordings, Authorized: true}
	if c.enhanced.enabled {
		p.DSPStore = c.analysis.store.DSP()
	}
	if clap := c.AudioService(); clap != nil {
		copy := clap.Clone()
		copy.DSPStore = p.DSPStore
		p = copy
	}
	if !c.enhanced.mertEnabled || c.enhanced.worker == nil || c.enhanced.manifest == nil {
		return p, nil
	}
	analyzer := ports.AudioRepresentationAnalyzer(c.enhanced.worker)
	if c.enhanced.pool != nil {
		analyzer = c.enhanced.pool
	}
	m := &audio.MERTService{Preview: p, Analyzer: analyzer, Store: c.analysis.store.Representations(), ParityValidated: c.enhanced.manifest.Parity.ValidForBackend(c.enhanced.manifest.Backend())}
	return p, m
}

func (c *Container) EnhancedPreviewService() *audio.Service {
	base := c.AudioService()
	c.enhanced.mu.Lock()
	defer c.enhanced.mu.Unlock()
	if (!c.enhanced.enabled && !c.enhanced.mertEnabled) || c.analysis.store == nil {
		return base
	}
	p, m := c.enhancedServices()
	p.MERT = m
	return p
}

// PrepareEnhancedAudio is called once before ranking. Only the first bounded
// candidates/references may trigger analysis; all remaining evidence is cached.
func (c *Container) PrepareEnhancedAudio(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile, refs []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
	return c.prepareEnhancedAudio(ctx, intent, profile, refs, true, nil)
}

// RefreshEnhancedAudio reads only completed compatible evidence after refills.
// It never admits tracks, downloads previews, rereads feedback, or restarts an
// inference budget. The generation's initial taste centroids remain unchanged.
func (c *Container) RefreshEnhancedAudio(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile, refs []core.TrackRef, previous *core.EnhancedAudioSnapshot) (*core.EnhancedAudioSnapshot, error) {
	return c.prepareEnhancedAudio(ctx, intent, profile, refs, false, previous)
}

func (c *Container) prepareEnhancedAudio(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile, refs []core.TrackRef, acquire bool, previous *core.EnhancedAudioSnapshot) (*core.EnhancedAudioSnapshot, error) {
	e := &c.enhanced
	if err := e.lockOperation(ctx); err != nil {
		return nil, err
	}
	defer e.opMu.Unlock()
	if (!e.enabled && !e.mertEnabled) || c.analysis.store == nil || intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil, nil
	}
	runtime := c.Runtime()
	if runtime.Resolver == nil {
		return nil, nil
	}
	catalog := runtime.Resolver.CatalogVersion()
	var model core.AudioRepresentationIdentity
	if e.mertEnabled && e.manifest != nil {
		model = e.manifest.Model
	}
	p, m := c.enhancedServices()
	snapshot, err := audio.PrepareEnhancedEvidence(ctx, p, m, catalog, model, refs, acquire, previous)
	if err != nil && (ctx.Err() != nil || snapshot == nil) {
		return snapshot, err
	}
	input := snapshot.Input()
	if acquire && m != nil && c.Feedback != nil {
		events, feedbackErr := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{RequestID: profile.RequestID, SessionID: profile.SessionID})
		if feedbackErr != nil {
			return nil, feedbackErr
		}
		cached := map[string]core.AudioRepresentation{}
		for _, event := range taste.ContentFeedback(events, profile) {
			if meta, ok := ports.CatalogMeta(ctx, runtime.Catalog, event.TrackID); ok {
				if a, found, err := m.Store.Find(ctx, catalog, event.TrackID, core.ProvisionalRecordingKey(meta.Ref), input.Model); err == nil && found {
					cached[event.TrackID] = a
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		// External representations belong to the base catalog. The persisted
		// profile retains its composite pack fingerprint for exact replay.
		sourceProfile := profile
		sourceProfile.CatalogVersion = catalog
		input.PositiveCentroid, input.NegativeCentroid = taste.ContentCentroids(events, sourceProfile, input.Model, cached)
	}
	return core.NewEnhancedAudioSnapshot(input)
}

func (c *Container) AnalyzeEnhancedTracks(ctx context.Context, ids []string, liked bool, p ports.Progress) (EnhancedAnalysisReport, error) {
	ctx = audio.WithEnhancedBudget(ctx, EnhancedAnalysisLimit, audio.EnhancedTimeLimit)
	ctx, cancel := audio.EnhancedBudgetFor(ctx).Context(ctx)
	defer cancel()
	e := &c.enhanced
	if err := e.lockOperation(ctx); err != nil {
		return EnhancedAnalysisReport{}, err
	}
	defer e.opMu.Unlock()
	report := EnhancedAnalysisReport{}
	if (!e.enabled && !e.mertEnabled) || c.analysis.store == nil {
		return report, fmt.Errorf("enable MERT similarity or DSP measurements first")
	}
	runtime := c.Runtime()
	if runtime.Catalog == nil || runtime.Resolver == nil {
		return report, core.ErrUnavailable
	}
	if liked && c.Feedback != nil {
		events, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{})
		if err != nil {
			return report, err
		}
		ids = nil
		for _, event := range taste.ContentFeedback(events, core.TasteProfile{}) {
			if event.Type == core.FeedbackLike || event.Type == core.FeedbackMoreLike {
				ids = append(ids, event.TrackID)
			}
		}
	}
	seen := map[string]bool{}
	refs := []core.TrackRef{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if meta, ok := ports.CatalogMeta(ctx, runtime.Catalog, id); ok {
			refs = append(refs, meta.Ref)
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if len(refs) >= EnhancedAnalysisLimit {
			break
		}
	}
	report.Requested = len(refs)
	preview, mert := c.enhancedServices()
	if mert == nil && preview.DSPStore == nil {
		return report, fmt.Errorf("install a ready MERT model before analyzing tracks")
	}
	for i, ref := range refs {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if p != nil {
			p.Report("enhanced-analysis", int64(i), int64(len(refs)), "Analyzing authorized previews locally")
		}
		var n int64
		var err error
		if mert != nil {
			_, _, n, err = mert.AnalyzeEnhancedPreview(ctx, ref, runtime.Resolver.CatalogVersion())
		} else {
			_, n, err = preview.AnalyzeDSPPreview(ctx, ref, runtime.Resolver.CatalogVersion())
		}
		report.Bytes += n
		if err != nil {
			report.Unavailable++
		} else {
			report.Analyzed++
		}
	}
	if p != nil {
		p.Report("enhanced-analysis", int64(len(refs)), int64(len(refs)), "Analysis complete; unavailable previews remain unknown")
	}
	return report, ctx.Err()
}
