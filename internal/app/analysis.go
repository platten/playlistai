package app

import (
	"context"
	"encoding/json"
	"errors"
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
	mu          sync.Mutex
	opMu        sync.Mutex
	startup     audioStartup
	store       *audio.Store
	bundles     *audio.BundleManager
	alternate   *audio.BundleManager
	worker      *audio.Worker
	pool        *audio.WorkerPool
	service     *audio.Service
	manifest    *audio.BundleManifest
	healthCheck func(context.Context, *audio.Worker) error // test seam
	enabled     bool
	detail      string
}

type AnalysisStatus struct {
	Loading              bool                      `json:"loading"`
	InstalledBackends    []string                  `json:"installedBackends"`
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

// AnalysisBundleOffer describes the recommended download without presenting
// partial artifact metadata as an installable bundle manifest.
type AnalysisBundleOffer struct {
	Label         string `json:"label"`
	Backend       string `json:"backend"`
	License       string `json:"license"`
	MemoryBytes   int64  `json:"memoryBytes"`
	DownloadBytes int64  `json:"downloadBytes"`
}

type analysisRecommendation struct {
	offer        AnalysisBundleOffer
	expected     audio.BundleManifest
	source       string
	distribution *modelpack.Distribution
}

func (c *Container) wireAnalysis(ctx context.Context) {
	s := &c.analysis
	s.bundles = &audio.BundleManager{Directory: filepath.Join(c.cfg.DataDir, "music-analysis")}
	s.alternate = &audio.BundleManager{Directory: filepath.Join(c.cfg.DataDir, "music-analysis-alternate")}
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
	if s.hasInstalledBundle() {
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
	started := time.Now()
	var dir string
	var manifest audio.BundleManifest
	var worker *audio.Worker
	var err error
	candidates := s.installedBundles(ctx, audio.MERTCUDAHostAvailable())
	verifiedLayout := time.Since(started)
	for _, candidate := range candidates {
		dir, manifest = candidate.dir, candidate.manifest
		worker = &audio.Worker{Executable: manifest.File(dir, "worker"), BundleDir: dir, Model: manifest.Model}
		healthTimeout := 60 * time.Second
		if manifest.Backend() == "cuda" {
			healthTimeout = 2 * time.Minute
		}
		healthCtx, cancel := context.WithTimeout(ctx, healthTimeout)
		if s.healthCheck != nil {
			err = s.healthCheck(healthCtx, worker)
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
		c.log.Info("CLAP startup check failed", "backend", manifest.Backend(), "health", time.Since(started))
	}
	if worker == nil {
		if err == nil {
			err = fmt.Errorf("no installed CLAP bundle is available")
		}
		return err
	}
	healthDuration := time.Since(started) - verifiedLayout
	pool := audio.NewWorkerPool(worker, audio.AnalysisParallelism())
	recordings := c.previewRecordings()
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
	c.log.Info("CLAP startup check complete", "layout", verifiedLayout, "health", healthDuration)
	return nil
}

func (c *Container) AudioService() *audio.Service {
	c.analysis.mu.Lock()
	if c.analysis.service == nil {
		c.analysis.mu.Unlock()
		return nil
	}
	var service *audio.Service
	if !c.analysis.enabled {
		if !c.analysis.service.InferenceReady() {
			c.analysis.mu.Unlock()
			return nil
		}
		// The installed model ranks best-available descriptions and screens vocals.
		// The setting controls additional calibrated musical-fit assessments.
		service = c.analysis.service.Clone()
		service.Policy = audio.Policy{}
	} else {
		service = c.analysis.service.Clone()
	}
	c.analysis.mu.Unlock()
	c.discogs.mu.Lock()
	if c.discogs.worker != nil && c.discogs.model != nil && c.analysis.store != nil {
		service.Classifier = &audio.DiscogsClassifier{Worker: c.discogs.worker, Model: *c.discogs.model, Store: c.analysis.store}
	}
	c.discogs.mu.Unlock()
	return service
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
	status := AnalysisStatus{Installed: s.manifest != nil, InstalledBackends: []string{}, Enabled: s.enabled, Available: s.service.InferenceReady(), GeneralFitAvailable: s.service.Ready(), Model: "Music CLAP", Detail: s.detail}
	for _, candidate := range s.installedBundles(ctx, true) {
		status.InstalledBackends = append(status.InstalledBackends, candidate.manifest.Backend())
	}
	status.Loading = s.startup.loading()
	recommended, recommendedErr := c.recommendedCLAP()
	status.RecommendedAvailable = recommendedErr == nil
	status.RecommendedInstalled = recommendedErr == nil && s.manifest != nil && s.manifest.Model == recommended.expected.Model
	if recommendedErr == nil {
		status.RecommendedManifest = recommended.source
		status.RecommendedBytes = recommended.offer.DownloadBytes
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

func (c *Container) recommendedCLAP() (analysisRecommendation, error) {
	return c.recommendedCLAPFor(audio.MERTCUDAHostAvailable())
}

func (c *Container) recommendedCLAPFor(cudaAvailable bool) (analysisRecommendation, error) {
	if cudaAvailable {
		if dir, manifest, ok := preferredCUDACLAP(); ok {
			return analysisRecommendation{offer: AnalysisBundleOffer{Label: manifest.Label, Backend: "cuda", License: manifest.License, MemoryBytes: manifest.MemoryBytes, DownloadBytes: manifest.DownloadBytes()}, expected: manifest, source: dir}, nil
		}
	}
	cpu, err := audio.RecommendedBundle()
	if err != nil {
		return analysisRecommendation{}, err
	}
	if cudaAvailable {
		if distribution, gpuErr := modelpack.RecommendedCLAPGPU(runtime.GOOS, runtime.GOARCH); gpuErr == nil {
			expected := cpu
			expected.ID = "custom-clap-cpu-v2-cuda-v1"
			if runtime.GOOS == "windows" {
				expected.ID = "custom-clap-cuda-v2"
			}
			expected.Model.Runtime = "onnxruntime/1.26.0/cuda"
			return analysisRecommendation{offer: AnalysisBundleOffer{Label: "LAION original HTSAT-base music checkpoint · CUDA", Backend: "cuda", License: "CC0-1.0 checkpoint; MIT ONNX Runtime; GPL-3.0 application worker; NVIDIA CUDA Toolkit and cuDNN licenses", MemoryBytes: 2500000000, DownloadBytes: distribution.DownloadBytes}, expected: expected, source: distribution.URL, distribution: &distribution}, nil
		}
	}
	distribution, err := modelpack.RecommendedCLAP(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return analysisRecommendation{}, err
	}
	return analysisRecommendation{offer: AnalysisBundleOffer{Label: cpu.Label, Backend: "cpu", License: cpu.License, MemoryBytes: cpu.MemoryBytes, DownloadBytes: distribution.DownloadBytes}, expected: cpu, source: distribution.URL, distribution: &distribution}, nil
}

func (c *Container) GetRecommendedAnalysisBundle() (AnalysisBundleOffer, error) {
	recommended, err := c.recommendedCLAP()
	return recommended.offer, err
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
	recommended, err := c.recommendedCLAP()
	if err != nil {
		return err
	}
	return c.installAnalysisSource(ctx, p, recommended)
}

// InstallCPUAnalysisBundle keeps a portable fallback beside the CUDA bundle.
func (c *Container) InstallCPUAnalysisBundle(ctx context.Context, p ports.Progress) error {
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
	return c.installAnalysisSource(ctx, p, analysisRecommendation{expected: recommended, source: distribution.URL, distribution: &distribution})
}

func (c *Container) installAnalysisSource(ctx context.Context, p ports.Progress, recommended analysisRecommendation) error {
	var directory string
	var cleanup func()
	var err error
	if recommended.source == "" {
		return fmt.Errorf("recommended CLAP source is unavailable")
	}
	if filepath.IsAbs(recommended.source) {
		directory, cleanup, err = c.prepareModelPack(ctx, recommended.source, "analysis-model", p)
	} else {
		if recommended.distribution == nil {
			return fmt.Errorf("recommended CLAP distribution is unavailable")
		}
		directory, cleanup, err = c.prepareRecommendedModelPack(ctx, *recommended.distribution, "analysis-model", p)
	}
	if err != nil {
		return err
	}
	defer cleanup()
	manifest, err := audio.ReadBundleContext(ctx, directory)
	if err != nil {
		return err
	}
	if manifest.ID != recommended.expected.ID || manifest.Model != recommended.expected.Model || manifest.EmbeddingFingerprint() != recommended.expected.EmbeddingFingerprint() {
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
	manager, err := c.analysis.managerFor(ctx, manifest.Backend())
	if err != nil {
		return err
	}
	if _, err := manager.Install(ctx, manifest, p); err != nil {
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
	manifest, err := audio.ReadBundleContext(ctx, directory)
	if err != nil {
		return err
	}
	manager, err := c.analysis.managerFor(ctx, manifest.Backend())
	if err != nil {
		return err
	}
	if _, err := manager.InstallDirectory(ctx, directory, p); err != nil {
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
	if c.analysis.alternate == nil {
		return c.analysis.bundles.Remove()
	}
	return errors.Join(c.analysis.bundles.Remove(), c.analysis.alternate.Remove())
}
