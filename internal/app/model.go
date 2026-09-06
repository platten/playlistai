package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/ports"
)

// modelStartTimeout bounds a whole model swap — it may try more than one
// runtime (GPU then CPU), each with runtimeStartTimeout to become healthy.
const modelStartTimeout = 6 * time.Minute

// runtimeStartTimeout bounds a single runtime's startup + health check.
const runtimeStartTimeout = 90 * time.Second

// llamaInstallTimeout bounds the official-installer runs (two downloads of a
// GPU/CPU build, tens to a few hundred MB each).
const llamaInstallTimeout = 20 * time.Minute

// modelVRAMReserve is retained after model weights to leave room for the
// prompt context, KV cache, and runtime compute buffers. It matches
// llama.cpp's default --fit margin.
const modelVRAMReserve int64 = 1 << 30

// llamaStageDir is where InstallLlamaRuntime stages the GPU + CPU builds.
func (c *Container) llamaStageDir() string {
	return filepath.Join(c.cfg.DataDir, "llama")
}

// LlamaRuntimes returns the runtimes to try, in order: the app-staged builds
// (GPU/primary, then CPU) if InstallLlamaRuntime has run, otherwise a single
// detected one (config path / PATH / a manual install).
func (c *Container) LlamaRuntimes() []llama.Runtime {
	if staged := llama.StagedRuntimes(c.llamaStageDir()); len(staged) > 0 {
		return staged
	}
	if rt := llama.DetectRuntime(c.cfg.AI.LlamaServerPath); rt.Available {
		return []llama.Runtime{{Path: rt.Path, Kind: rt.Kind}}
	}
	return nil
}

// LlamaRuntime reports whether any llama.cpp runtime is available (the first
// of LlamaRuntimes), and — when it's an app-staged one — which builds are
// present ("gpu", "cpu").
func (c *Container) LlamaRuntime() (st llama.RuntimeStatus, builds []string) {
	rts := c.LlamaRuntimes()
	if len(rts) == 0 {
		return llama.RuntimeStatus{}, nil
	}
	src := "staged"
	if rts[0].Label == "" {
		src = "detected"
	}
	for _, r := range rts {
		if r.Label != "" {
			builds = append(builds, r.Label)
		}
	}
	return llama.RuntimeStatus{Available: true, Path: rts[0].Path, Kind: rts[0].Kind, Source: src}, builds
}

// LlamaHardware probes GPU support through the exact llama.cpp runtime the app
// would use. It returns the single device with the most VRAM because wizard
// recommendations require the complete model to fit on one GPU.
func (c *Container) LlamaHardware(ctx context.Context) (device llama.Device, available bool) {
	for _, rt := range c.LlamaRuntimes() {
		if rt.Label == "cpu" {
			continue
		}
		devices, err := llama.ProbeDevices(ctx, rt)
		if err != nil {
			c.log.Debug("llama device probe failed", "runtime", rt.Path, "err", err)
			continue
		}
		for _, candidate := range devices {
			if !available || candidate.TotalBytes > device.TotalBytes {
				device, available = candidate, true
			}
		}
		if available {
			return device, true
		}
	}
	return llama.Device{}, false
}

// ModelVRAMReserve returns the capacity intentionally excluded from model
// weight fit calculations for context/KV and compute buffers.
func (c *Container) ModelVRAMReserve() int64 { return modelVRAMReserve }

// InstallLlamaRuntime runs ggml-org's official installer, staging a
// GPU-capable build (CUDA / ROCm / Vulkan / Metal) and — on Linux/Windows — a
// CPU fallback build entirely under the app's data dir. When reinstall is
// true any existing install is deleted first. Progress is reported via p
// under op "llama-install" as an indeterminate ("bouncing") bar with the
// current phase + installer output as the note.
func (c *Container) InstallLlamaRuntime(ctx context.Context, p ports.Progress, reinstall bool) error {
	if p == nil {
		p = ports.NopProgress{}
	}
	if reinstall {
		p.Report("llama-install", 0, -1, "removing the current llama.cpp…")
		llama.CleanStaged(c.llamaStageDir())
	}
	ictx, cancel := context.WithTimeout(ctx, llamaInstallTimeout)
	defer cancel()

	p.Report("llama-install", 0, -1, "starting the llama.cpp installer…")
	err := llama.InstallOfficial(ictx, c.llamaStageDir(), func(step, steps int, line string) {
		line = trimLine(line)
		if line != "" {
			c.log.Info("llama-install", "step", step, "line", line)
		}
		phase := fmt.Sprintf("step %d of %d", step, steps)
		note := phase
		if line != "" && line != "ready" {
			note = phase + " · " + line
		}
		// total < 0 → the frontend renders a bouncing indeterminate bar.
		p.Report("llama-install", 0, -1, note)
	})
	if err != nil {
		return fmt.Errorf("app: llama.cpp install: %w", err)
	}
	if len(c.LlamaRuntimes()) == 0 {
		return fmt.Errorf("app: installer finished but no llama runtime was staged")
	}
	c.log.Info("llama.cpp installed", "runtimes", len(c.LlamaRuntimes()))
	return nil
}

func trimLine(s string) string {
	// Drop the installer's carriage-return progress spinners.
	if len(s) > 0 && (s[0] == '\r' || s[len(s)-1] == '%') {
		return ""
	}
	return s
}

// CurrentModel returns the active model's path and catalog id (both empty when
// the rules parser is in use).
func (c *Container) CurrentModel() (path, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modelPath, c.modelID
}

// SetModel starts llama-server on the given GGUF and makes it the active parser,
// persisting the choice. On any failure the current parser is left untouched.
func (c *Container) SetModel(ctx context.Context, modelPath, modelID string) error {
	if err := modelmgr.ValidateGGUF(modelPath); err != nil {
		return err
	}

	sctx, cancel := context.WithTimeout(ctx, modelStartTimeout)
	defer cancel()

	p, err := llama.New(sctx, llama.Options{
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
		return err
	}

	c.setLlama(p, modelPath, modelID)
	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.ModelPath, prefs.ModelID = modelPath, modelID
	if serr := prefs.Save(c.cfg.DataDir); serr != nil {
		c.log.Warn("could not persist model choice", "err", serr)
	}
	c.log.Info("model set", "path", modelPath, "id", modelID)
	return nil
}

// DownloadModel fetches a catalog model and switches to it. Progress is reported
// under modelmgr.ProgressOp.
func (c *Container) DownloadModel(ctx context.Context, id string, p ports.Progress) error {
	m, ok := modelmgr.Get(id)
	if !ok {
		return fmt.Errorf("app: unknown model %q", id)
	}
	dir := filepath.Join(c.cfg.DataDir, "models")
	path, err := modelmgr.Download(ctx, m, dir, p)
	if err != nil {
		return err
	}
	return c.SetModel(ctx, path, id)
}

// ClearModel stops llama-server and reverts to the rules parser.
func (c *Container) ClearModel() error {
	c.setLlama(nil, "", "")
	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.ModelPath, prefs.ModelID = "", ""
	if serr := prefs.Save(c.cfg.DataDir); serr != nil {
		c.log.Warn("could not persist model choice", "err", serr)
	}
	c.log.Info("model cleared; using rules parser")
	return nil
}

// setLlama swaps the active parser and closes any previously-running
// llama-server. Passing nil reverts to the rules parser.
func (c *Container) setLlama(p *llama.Parser, modelPath, modelID string) {
	c.mu.Lock()
	old := c.llama
	c.llama = p
	c.modelPath, c.modelID = modelPath, modelID
	if p != nil {
		c.parser = p
	} else {
		c.parser = c.rulesParser
	}
	c.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
}
