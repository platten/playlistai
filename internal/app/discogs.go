package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/musicgraph"
	"github.com/platten/playlistai/internal/ports"
)

type discogsState struct {
	mu               sync.Mutex
	opMu             sync.Mutex
	startup          audioStartup
	worker           *audio.DiscogsWorker
	model            *audio.DiscogsModel
	detail           string
	workerExecutable string // isolated native smoke tests
}

type DiscogsStatus struct {
	Loading              bool   `json:"loading"`
	Installed            bool   `json:"installed"`
	Available            bool   `json:"available"`
	RecommendedAvailable bool   `json:"recommendedAvailable"`
	DownloadBytes        int64  `json:"downloadBytes"`
	Detail               string `json:"detail"`
	License              string `json:"license"`
}

func (c *Container) discogsDir() string {
	return filepath.Join(c.cfg.DataDir, "music-classifiers", "discogs-effnet-v1")
}

func (c *Container) wireDiscogs(ctx context.Context) {
	c.discogs.detail = "Install the original Discogs-EffNet ONNX models for local estimated instrument, vocal, relaxed-mood, and style scores."
	c.RegisterCloser(func() error {
		c.discogs.mu.Lock()
		defer c.discogs.mu.Unlock()
		if c.discogs.worker != nil {
			return c.discogs.worker.Close()
		}
		return nil
	})
	if _, err := os.Stat(c.discogsDir()); err == nil {
		c.discogs.startup.start(c, ctx, func(ctx context.Context) {
			c.discogs.opMu.Lock()
			defer c.discogs.opMu.Unlock()
			for c.analysis.startup.loading() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
			_ = c.loadDiscogs(ctx)
		})
	}
}

func (c *Container) discogsRuntimeDir() string {
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	if c.analysis.worker == nil || c.analysis.manifest == nil {
		return ""
	}
	return c.analysis.worker.BundleDir
}

func (c *Container) loadDiscogs(ctx context.Context) error {
	model, err := audio.ReadDiscogsModel(ctx, c.discogsDir())
	if err != nil {
		c.discogs.mu.Lock()
		c.discogs.detail = "Installed Discogs assets failed verification. Remove and download them again."
		c.discogs.mu.Unlock()
		return err
	}
	runtimeDir := c.discogsRuntimeDir()
	if runtimeDir == "" {
		c.discogs.mu.Lock()
		c.discogs.detail = "Install and validate CLAP first; its native runtime also runs Discogs-EffNet."
		c.discogs.mu.Unlock()
		return fmt.Errorf("validated CLAP runtime required")
	}
	worker := &audio.DiscogsWorker{ModelDir: c.discogsDir(), RuntimeDir: runtimeDir, Executable: c.discogs.workerExecutable}
	healthCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := worker.Health(healthCtx); err != nil {
		_ = worker.Close()
		c.discogs.mu.Lock()
		c.discogs.detail = "Discogs native inference failed its health check. Retry the download."
		c.discogs.mu.Unlock()
		return err
	}
	c.discogs.mu.Lock()
	defer c.discogs.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = worker.Close()
		return err
	}
	if c.discogs.worker != nil {
		_ = c.discogs.worker.Close()
	}
	c.discogs.worker, c.discogs.model = worker, &model
	c.discogs.detail = "Discogs-EffNet is ready for local estimated classification. Scores describe sampled audio and do not certify strict requirements."
	return nil
}

func (c *Container) GetDiscogsStatus(ctx context.Context) (DiscogsStatus, error) {
	c.discogs.mu.Lock()
	s := DiscogsStatus{Loading: c.discogs.startup.loading(), Available: c.discogs.worker != nil, Detail: c.discogs.detail, DownloadBytes: audio.DiscogsDownloadBytes(), License: "Noncommercial; Essentia model license notices differ between assets"}
	c.discogs.mu.Unlock()
	s.RecommendedAvailable = audio.NativeInferenceAvailable() && c.discogsRuntimeDir() != ""
	s.Available = s.Available && s.RecommendedAvailable
	_, err := audio.ReadDiscogsModel(ctx, c.discogsDir())
	s.Installed = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Available = false
		s.Detail = "Discogs model files failed verification. Download and validate them again."
	}
	if !s.RecommendedAvailable && !s.Loading {
		if !audio.NativeInferenceAvailable() {
			s.Detail = "This build does not include native audio inference. Install a native-analysis-enabled build to use Discogs-EffNet."
		} else {
			s.Detail = "Install and validate CLAP first; its native runtime is required for Discogs-EffNet."
		}
	}
	return s, ctx.Err()
}

func (c *Container) InstallDiscogs(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.discogs.startup.stop()
	c.discogs.opMu.Lock()
	defer c.discogs.opMu.Unlock()
	if !audio.NativeInferenceAvailable() || c.discogsRuntimeDir() == "" {
		return fmt.Errorf("install a validated native CLAP model first")
	}
	cache := filepath.Join(c.cfg.DataDir, "model-downloads", "discogs-effnet-v1")
	if err := os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	var completed int64
	for _, file := range audio.DiscogsFiles {
		target := filepath.Join(cache, file.Name)
		if dataset.VerifyFile(ctx, target, file.Size, file.SHA256) == nil {
			completed += file.Size
			if p != nil {
				p.Report("discogs-model", completed, audio.DiscogsDownloadBytes(), "Reusing verified Discogs-EffNet download")
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_, err := dataset.Download(ctx, file.URL, target, file.Size, file.SHA256, func(done, _ int64) {
			if p != nil {
				p.Report("discogs-model", completed+done, audio.DiscogsDownloadBytes(), "Downloading original Discogs-EffNet models")
			}
		})
		if err != nil {
			return err
		}
		completed += file.Size
	}
	unlock, err := c.preparedInstallGuard(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	existing, err := c.additionalPreparedBytes(ctx)
	if err != nil {
		return err
	}
	if err := musicgraph.CheckAdditionalDataBudget(existing, audio.DiscogsDownloadBytes()); err != nil {
		return err
	}
	root := filepath.Dir(c.discogsDir())
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, ".discogs-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, file := range audio.DiscogsFiles {
		in, err := os.Open(filepath.Join(cache, file.Name))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(stage, file.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		err = errors.Join(err, out.Close(), in.Close())
		if err != nil {
			return err
		}
	}
	if _, err := audio.ReadDiscogsModel(ctx, stage); err != nil {
		return err
	}
	if err := os.Rename(stage, c.discogsDir()); err != nil {
		if _, existingErr := audio.ReadDiscogsModel(ctx, c.discogsDir()); existingErr == nil {
			return c.loadDiscogs(ctx)
		}
		backup := filepath.Join(root, fmt.Sprintf(".discogs-old-%d", time.Now().UnixNano()))
		if moveErr := os.Rename(c.discogsDir(), backup); moveErr != nil {
			return errors.Join(err, moveErr)
		}
		if moveErr := os.Rename(stage, c.discogsDir()); moveErr != nil {
			_ = os.Rename(backup, c.discogsDir())
			return moveErr
		}
		_ = os.RemoveAll(backup)
	}
	if p != nil {
		p.Report("discogs-model", audio.DiscogsDownloadBytes(), audio.DiscogsDownloadBytes(), "Validating native Discogs inference")
	}
	if err := c.loadDiscogs(ctx); err != nil {
		return err
	}
	if p != nil {
		p.Report("discogs-model", audio.DiscogsDownloadBytes(), audio.DiscogsDownloadBytes(), "Discogs-EffNet ready")
	}
	return nil
}

func (c *Container) RemoveDiscogs() error {
	c.discogs.startup.stop()
	c.discogs.opMu.Lock()
	defer c.discogs.opMu.Unlock()
	c.discogs.mu.Lock()
	if c.discogs.worker != nil {
		_ = c.discogs.worker.Close()
	}
	c.discogs.worker, c.discogs.model = nil, nil
	c.discogs.detail = "Discogs-EffNet is not installed."
	c.discogs.mu.Unlock()
	return errors.Join(os.RemoveAll(c.discogsDir()), os.RemoveAll(filepath.Join(c.cfg.DataDir, "model-downloads", "discogs-effnet-v1")))
}
