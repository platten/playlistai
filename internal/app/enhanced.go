package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/taste"
)

const EnhancedAnalysisLimit = audio.EnhancedTrackLimit

type enhancedState struct {
	mu       sync.Mutex
	opMu     sync.Mutex
	enabled  bool
	bundles  *audio.MERTBundleManager
	worker   *audio.MERTWorker
	manifest *audio.MERTBundleManifest
	detail   string
}

type EnhancedAnalysisStatus struct {
	Enabled       bool                                 `json:"enabled"`
	DSPAvailable  bool                                 `json:"dspAvailable"`
	MERTAvailable bool                                 `json:"mertAvailable"`
	Installed     bool                                 `json:"installed"`
	Model         string                               `json:"model"`
	Revision      string                               `json:"revision"`
	License       string                               `json:"license"`
	DownloadBytes int64                                `json:"downloadBytes"`
	DSPStorage    core.DSPStorageUsage                 `json:"dspStorage"`
	MERTStorage   core.AudioRepresentationStorageUsage `json:"mertStorage"`
	Detail        string                               `json:"detail"`
	Limit         int                                  `json:"limit"`
}

type EnhancedAnalysisReport struct {
	Requested   int   `json:"requested"`
	Analyzed    int   `json:"analyzed"`
	Unavailable int   `json:"unavailable"`
	Bytes       int64 `json:"bytes"`
}

func (c *Container) wireEnhanced(ctx context.Context) {
	e := &c.enhanced
	e.enabled = config.LoadPrefs(c.cfg.DataDir).EnhancedAudioEnabled
	e.bundles = &audio.MERTBundleManager{Directory: filepath.Join(c.cfg.DataDir, "mert-analysis")}
	e.detail = "DSP needs no model. MERT is an optional, separately licensed audio representation."
	if c.analysis.store == nil {
		return
	}
	if _, _, err := e.bundles.Active(); err == nil {
		if err := c.loadMERT(ctx); err != nil {
			e.detail = "Installed MERT failed its health check. DSP remains available."
		}
	}
	c.RegisterCloser(func() error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.worker != nil {
			return e.worker.Close()
		}
		return nil
	})
}

func (c *Container) loadMERT(ctx context.Context) error {
	e := &c.enhanced
	dir, manifest, err := e.bundles.Active()
	if err != nil {
		return err
	}
	worker := &audio.MERTWorker{BundleDir: dir, Model: manifest.Model}
	healthCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := worker.Health(healthCtx); err != nil {
		_ = worker.Close()
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.worker != nil {
		_ = e.worker.Close()
	}
	e.worker, e.manifest = worker, &manifest
	e.detail = "MERT compares audio with audio, not with text. Preview measurements describe only the analyzed interval."
	if !e.enabled {
		worker.Unload()
	}
	return nil
}

func (c *Container) GetEnhancedAnalysisStatus(ctx context.Context) (EnhancedAnalysisStatus, error) {
	e := &c.enhanced
	e.mu.Lock()
	defer e.mu.Unlock()
	s := EnhancedAnalysisStatus{Enabled: e.enabled, DSPAvailable: c.analysis.store != nil, Installed: e.manifest != nil, MERTAvailable: e.worker != nil && audio.NativeInferenceAvailable(), Model: "MERT-v1-95M", License: "CC-BY-NC-4.0 (noncommercial)", Detail: e.detail, Limit: EnhancedAnalysisLimit}
	if e.manifest != nil {
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
	prefs.EnhancedAudioEnabled = enabled
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	e.enabled = enabled
	if !enabled && e.worker != nil {
		e.worker.Unload()
	}
	return nil
}

// InstallMERT imports a maintainer-prepared pack with verified hashes. The pack
// includes the native runtime for this OS/architecture and never requires Python.
func (c *Container) InstallMERT(ctx context.Context, directory string, p ports.Progress) error {
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if c.analysis.store == nil {
		return fmt.Errorf("enhanced analysis storage unavailable")
	}
	directory, cleanup, err := c.prepareModelPack(ctx, directory, "mert-model", p)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := e.bundles.InstallLocal(ctx, directory, p); err != nil {
		return err
	}
	return c.loadMERT(ctx)
}

func (c *Container) RemoveMERT() error {
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.worker != nil {
		_ = e.worker.Close()
	}
	e.worker = nil
	e.manifest = nil
	if e.bundles == nil {
		return nil
	}
	return e.bundles.Remove()
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

func (c *Container) enhancedServices() (*audio.Service, *audio.MERTService) {
	var recordings ports.CachedRecordingReader
	if r, ok := c.Enrich.(ports.CachedRecordingReader); ok {
		recordings = r
	}
	p := &audio.Service{Resolver: deezer.New(deezer.Config{}), Recordings: recordings, Authorized: true, DSPStore: c.analysis.store.DSP()}
	if clap := c.AudioService(); clap != nil {
		copy := *clap
		copy.DSPStore = p.DSPStore
		p = &copy
	}
	if c.enhanced.worker == nil || c.enhanced.manifest == nil {
		return p, nil
	}
	m := &audio.MERTService{Preview: p, Analyzer: c.enhanced.worker, Store: c.analysis.store.Representations(), ParityValidated: c.enhanced.manifest.Parity.Valid()}
	return p, m
}

func (c *Container) EnhancedPreviewService() *audio.Service {
	base := c.AudioService()
	if base == nil {
		return nil
	}
	c.enhanced.mu.Lock()
	defer c.enhanced.mu.Unlock()
	if !c.enhanced.enabled || c.analysis.store == nil {
		return base
	}
	p := *base
	p.DSPStore = c.analysis.store.DSP()
	_, p.MERT = c.enhancedServices()
	return &p
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
	if acquire {
		ctx = audio.WithLazyEnhancedBudget(ctx, EnhancedAnalysisLimit, audio.EnhancedTimeLimit)
	}
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if !e.enabled || c.analysis.store == nil || intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil, nil
	}
	runtime := c.Runtime()
	if runtime.Resolver == nil {
		return nil, nil
	}
	catalog := runtime.Resolver.CatalogVersion()
	input := core.EnhancedAudioInput{CatalogVersion: catalog, DSPVersion: audio.DSPAnalysisVersion, DSP: map[string]core.DSPAnalysis{}, Representations: map[string]core.AudioRepresentation{}}
	if e.manifest != nil {
		input.Model = e.manifest.Model
	}
	if !acquire && previous != nil {
		prior := previous.Input()
		if prior.CatalogVersion != input.CatalogVersion || prior.Model != input.Model || prior.PolicyVersion != core.EnhancedAudioPolicyVersion {
			return nil, fmt.Errorf("enhanced audio refresh requires the same catalog, model and policy as the initial snapshot")
		}
		input.PositiveCentroid, input.NegativeCentroid = prior.PositiveCentroid, prior.NegativeCentroid
	}
	p, m := c.enhancedServices()
	seen := map[string]bool{}
	budget := audio.EnhancedBudgetFor(ctx)
	for _, ref := range refs {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		if err := ctx.Err(); err != nil {
			completed, freezeErr := core.NewEnhancedAudioSnapshot(input)
			if freezeErr != nil {
				return nil, freezeErr
			}
			return completed, err
		}
		dspCached, dspHit, _ := p.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), audio.DSPAnalysisVersion)
		if dspHit {
			input.DSP[ref.ID] = dspCached
		}
		mertHit := m == nil
		if m != nil {
			cached, hit, _ := m.Store.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), input.Model)
			mertHit = hit
			if hit {
				input.Representations[ref.ID] = cached
			}
		}
		if acquire && (!dspHit || !mertHit) && ctx.Err() == nil && budget.Allow(ref.ID) {
			budgetCtx, cancel := budget.Context(ctx)
			if m != nil {
				_, _, _, _ = m.AnalyzeEnhancedPreview(budgetCtx, ref, catalog)
			} else {
				_, _, _ = p.AnalyzeDSPPreview(budgetCtx, ref, catalog)
			}
			cancel()
		}
		if a, ok, err := p.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), audio.DSPAnalysisVersion); err == nil && ok {
			input.DSP[ref.ID] = a
		}
		if m != nil {
			if a, ok, err := m.Store.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), input.Model); err == nil && ok {
				input.Representations[ref.ID] = a
			}
		}
	}
	if err := ctx.Err(); err != nil {
		completed, freezeErr := core.NewEnhancedAudioSnapshot(input)
		if freezeErr != nil {
			return nil, freezeErr
		}
		return completed, err
	}
	if acquire && m != nil && c.Feedback != nil {
		events, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{RequestID: profile.RequestID, SessionID: profile.SessionID})
		if err != nil {
			return nil, err
		}
		cached := map[string]core.AudioRepresentation{}
		for _, event := range taste.ContentFeedback(events, profile) {
			if meta, ok := runtime.Catalog.Meta(event.TrackID); ok {
				if a, found, err := m.Store.Find(ctx, catalog, event.TrackID, core.ProvisionalRecordingKey(meta.Ref), input.Model); err == nil && found {
					cached[event.TrackID] = a
				}
			}
		}
		input.PositiveCentroid, input.NegativeCentroid = taste.ContentCentroids(events, profile, input.Model, cached)
	}
	return core.NewEnhancedAudioSnapshot(input)
}

func (c *Container) AnalyzeEnhancedTracks(ctx context.Context, ids []string, liked bool, p ports.Progress) (EnhancedAnalysisReport, error) {
	ctx = audio.WithEnhancedBudget(ctx, EnhancedAnalysisLimit, audio.EnhancedTimeLimit)
	ctx, cancel := audio.EnhancedBudgetFor(ctx).Context(ctx)
	defer cancel()
	e := &c.enhanced
	e.opMu.Lock()
	defer e.opMu.Unlock()
	report := EnhancedAnalysisReport{}
	if !e.enabled || c.analysis.store == nil {
		return report, fmt.Errorf("enable enhanced audio analysis first")
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
		if meta, ok := runtime.Catalog.Meta(id); ok {
			refs = append(refs, meta.Ref)
		}
		if len(refs) >= EnhancedAnalysisLimit {
			break
		}
	}
	report.Requested = len(refs)
	preview, mert := c.enhancedServices()
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
