package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/modelpack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
)

type analysisState struct {
	mu       sync.Mutex
	opMu     sync.Mutex
	startup  audioStartup
	store    *audio.Store
	bundles  *audio.BundleManager
	worker   *audio.Worker
	pool     *audio.WorkerPool
	service  *audio.Service
	manifest *audio.BundleManifest
	enabled  bool
	detail   string
}

type AnalysisStatus struct {
	Loading              bool                      `json:"loading"`
	RecommendedAvailable bool                      `json:"recommendedAvailable"`
	RecommendedInstalled bool                      `json:"recommendedInstalled"`
	RecommendedDetail    string                    `json:"recommendedDetail"`
	RecommendedManifest  string                    `json:"recommendedManifest"`
	RecommendedBytes     int64                     `json:"recommendedBytes"`
	Installed            bool                      `json:"installed"`
	Available            bool                      `json:"available"`
	GeneralFitAvailable  bool                      `json:"generalFitAvailable"`
	Enabled              bool                      `json:"enabled"`
	Model                string                    `json:"model"`
	Detail               string                    `json:"detail"`
	DownloadBytes        int64                     `json:"downloadBytes"`
	MemoryBytes          int64                     `json:"memoryBytes"`
	Storage              core.AnalysisStorageUsage `json:"storage"`
}

func (c *Container) wireAnalysis(ctx context.Context) {
	s := &c.analysis
	s.bundles = &audio.BundleManager{Directory: filepath.Join(c.cfg.DataDir, "music-analysis")}
	s.enabled = config.LoadPrefs(c.cfg.DataDir).AnalysisEnabled
	s.detail = "Download the recommended CLAP model or choose a compatible custom bundle."
	store, err := audio.OpenStore(c.cfg.DataDir)
	if err != nil {
		s.detail = "Local analysis storage is unavailable."
		return
	}
	s.store = store
	c.RegisterCloser(store.Close)
	c.RegisterCloser(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.pool != nil {
			return s.pool.Close()
		}
		if s.worker != nil {
			return s.worker.Close()
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(s.bundles.Directory, "active.json")); err == nil {
		s.startup.start(c, ctx, func(ctx context.Context) {
			s.opMu.Lock()
			defer s.opMu.Unlock()
			if err := c.loadAnalysis(ctx); err != nil && ctx.Err() == nil {
				s.mu.Lock()
				s.detail = "The installed analysis bundle failed its health check. Retry installation."
				s.mu.Unlock()
			}
		})
	}
}

func (c *Container) loadAnalysis(ctx context.Context) error {
	s := &c.analysis
	dir, manifest, err := s.bundles.ActiveContext(ctx)
	if err != nil {
		return err
	}
	worker := &audio.Worker{Executable: manifest.File(dir, "worker"), BundleDir: dir, Model: manifest.Model}
	healthCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := worker.Health(healthCtx); err != nil {
		_ = worker.Close()
		return err
	}
	pool := audio.NewWorkerPool(worker, audio.AnalysisParallelism())
	var recordings ports.CachedRecordingReader
	if r, ok := c.Enrich.(ports.CachedRecordingReader); ok {
		recordings = r
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = pool.Close()
		return err
	}
	if s.pool != nil {
		_ = s.pool.Close()
	} else if s.worker != nil {
		_ = s.worker.Close()
	}
	s.worker = worker
	s.pool = pool
	s.manifest = &manifest
	// Provider permission for analysis, persistent derivatives and distributed
	// users was confirmed by the project owner for this implementation.
	s.service = &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: pool, Store: s.store, Recordings: recordings, Policy: manifest.Policy, Authorized: true, ParityValidated: manifest.Parity.Valid()}
	if !s.enabled {
		pool.Unload()
	}
	s.detail = "Preview audio is processed in memory. Derived features stay on this device until cleared. Assessments cover the preview only."
	if !manifest.Policy.Valid() {
		s.enabled = false
		pool.Unload()
		s.detail = "CLAP compares previews with your description to help rank tracks and screens no-vocals requests. Similarity scores are not calibrated judgments of musical fit."
	}
	return nil
}

func (c *Container) AudioService() *audio.Service {
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	if !c.analysis.enabled {
		if !c.analysis.service.InferenceReady() {
			return nil
		}
		// The installed model ranks best-available descriptions and screens vocals.
		// The setting controls additional calibrated musical-fit assessments.
		service := c.analysis.service.Clone()
		service.Policy = audio.Policy{}
		return service
	}
	return c.analysis.service
}

func (c *Container) ProposeAnchors(ctx context.Context, intent core.MusicIntent, rejected []string) ([]core.InferredAnchor, error) {
	if parser, ok := c.IntentParser().(interface {
		ProposeAnchors(context.Context, core.MusicIntent, []string) ([]core.InferredAnchor, error)
	}); ok {
		return parser.ProposeAnchors(ctx, intent, rejected)
	}
	return nil, core.ErrUnavailable
}

func (c *Container) GetAnalysisStatus(ctx context.Context) (AnalysisStatus, error) {
	s := &c.analysis
	s.mu.Lock()
	defer s.mu.Unlock()
	status := AnalysisStatus{Installed: s.manifest != nil, Enabled: s.enabled, Available: s.service.InferenceReady(), GeneralFitAvailable: s.service.Ready(), Model: "Music CLAP · CPU", Detail: s.detail}
	status.Loading = s.startup.loading()
	recommended, recommendedErr := audio.RecommendedBundle()
	distribution, distributionErr := modelpack.RecommendedCLAP(runtime.GOOS, runtime.GOARCH)
	status.RecommendedAvailable = recommendedErr == nil && distributionErr == nil
	status.RecommendedInstalled = recommendedErr == nil && s.manifest != nil && s.manifest.Model == recommended.Model
	if distributionErr == nil {
		status.RecommendedManifest = distribution.URL
		status.RecommendedBytes = distribution.DownloadBytes
	}
	if !status.RecommendedAvailable {
		status.RecommendedDetail = "No recommended music analysis bundle is available for this platform. Choose a compatible custom bundle, or continue without analysis."
		if !audio.NativeInferenceAvailable() {
			status.RecommendedDetail = "This build cannot run the recommended music analysis model. Install a native-analysis-enabled build, or continue without analysis. Models alone cannot add the missing application worker."
		}
		if !status.Installed {
			status.Detail = "Optional music analysis is unavailable in this build. Catalog recommendations remain available."
		}
	}
	if s.manifest != nil {
		status.Model = s.manifest.Label
		status.DownloadBytes = s.manifest.DownloadBytes()
		status.MemoryBytes = s.manifest.MemoryBytes
	}
	if s.store != nil {
		usage, err := s.store.Usage(ctx)
		status.Storage = usage
		return status, err
	}
	return status, nil
}

// InspectAnalysisBundle reads an operator-supplied bundle manifest. Public
// downloads become offerable only when a reviewed, pinned export exists.
func (c *Container) InspectAnalysisBundle(path string) (audio.BundleManifest, error) {
	var manifest audio.BundleManifest
	file, err := os.Open(path)
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return manifest, fmt.Errorf("analysis: invalid manifest size")
	}
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return manifest, err
	}
	return manifest, manifest.Validate()
}

func (c *Container) InstallAnalysisBundle(ctx context.Context, path string, p ports.Progress) error {
	manifest, err := c.InspectAnalysisBundle(path)
	if err != nil {
		return err
	}
	return c.installAnalysisManifest(ctx, manifest, p)
}

func (c *Container) InstallRecommendedAnalysisBundle(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.analysis.startup.stop()
	c.analysis.opMu.Lock()
	defer c.analysis.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.analysis.store == nil {
		return fmt.Errorf("analysis storage unavailable")
	}
	recommended, err := audio.RecommendedBundle()
	if err != nil {
		return err
	}
	distribution, err := modelpack.RecommendedCLAP(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	directory, cleanup, err := c.prepareRecommendedModelPack(ctx, distribution, "analysis-model", p)
	if err != nil {
		return err
	}
	defer cleanup()
	manifest, err := audio.ReadBundleContext(ctx, directory)
	if err != nil {
		return err
	}
	if manifest.ID != recommended.ID || manifest.Model != recommended.Model || manifest.EmbeddingFingerprint() != recommended.EmbeddingFingerprint() {
		return fmt.Errorf("downloaded analysis model does not match the recommended CLAP identity")
	}
	return c.installAnalysisDirectoryPrepared(ctx, directory, p)
}

func (c *Container) installAnalysisManifest(ctx context.Context, manifest audio.BundleManifest, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.analysis.startup.stop()
	c.analysis.opMu.Lock()
	defer c.analysis.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.analysis.store == nil {
		return fmt.Errorf("analysis storage unavailable")
	}
	if _, err := c.analysis.bundles.Install(ctx, manifest, p); err != nil {
		return err
	}
	return c.loadAnalysis(ctx)
}

func (c *Container) installAnalysisDirectoryPrepared(ctx context.Context, directory string, p ports.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.analysis.store == nil {
		return fmt.Errorf("analysis storage unavailable")
	}
	if _, err := c.analysis.bundles.InstallDirectory(ctx, directory, p); err != nil {
		return err
	}
	return c.loadAnalysis(ctx)
}

func (c *Container) SetAnalysisEnabled(enabled bool) error {
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	if enabled && !c.analysis.service.Ready() {
		return fmt.Errorf("install a validated analysis bundle first")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.AnalysisEnabled = enabled
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	c.analysis.enabled = enabled
	if !enabled && c.analysis.pool != nil {
		c.analysis.pool.Unload()
	} else if !enabled && c.analysis.worker != nil {
		c.analysis.worker.Unload()
	}
	return nil
}

func (c *Container) ClearAnalysis(ctx context.Context) error {
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	if c.analysis.store == nil {
		return nil
	}
	return c.analysis.store.Clear(ctx)
}

func (c *Container) RemoveAnalysisModel() error {
	c.analysis.startup.stop()
	c.analysis.opMu.Lock()
	defer c.analysis.opMu.Unlock()
	if err := c.SetAnalysisEnabled(false); err != nil {
		return err
	}
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	if c.analysis.pool != nil {
		_ = c.analysis.pool.Close()
	} else if c.analysis.worker != nil {
		_ = c.analysis.worker.Close()
	}
	c.analysis.worker = nil
	c.analysis.pool = nil
	c.analysis.service = nil
	c.analysis.manifest = nil
	c.analysis.detail = "Music analysis model removed. Derived features are retained until explicitly cleared."
	return c.analysis.bundles.Remove()
}
