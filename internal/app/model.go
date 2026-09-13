package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

// LlamaDevices probes GPU support through the exact llama.cpp runtime the app
// would use. The ids are suitable for llama.cpp's --device argument.
func (c *Container) LlamaDevices(ctx context.Context) []llama.Device {
	if c.deviceProber != nil {
		return c.deviceProber(ctx)
	}
	for _, rt := range c.LlamaRuntimes() {
		if rt.Label == "cpu" {
			continue
		}
		devices, err := llama.ProbeDevices(ctx, rt)
		if err != nil {
			c.log.Debug("llama device probe failed", "runtime", rt.Path, "err", err)
			continue
		}
		if len(devices) > 0 {
			return devices
		}
	}
	return nil
}

// LlamaHardware retains the single-device view used by older callers.
func (c *Container) LlamaHardware(ctx context.Context) (llama.Device, bool) {
	return PreferredLlamaDevice(c.LlamaDevices(ctx))
}

// PreferredLlamaDevice selects an NVIDIA/CUDA device by default, then the
// device with the greatest currently usable memory within that class.
func PreferredLlamaDevice(devices []llama.Device) (llama.Device, bool) {
	var selected llama.Device
	available := false
	selectedNVIDIA := false
	for _, candidate := range devices {
		nvidia := strings.HasPrefix(strings.ToUpper(candidate.ID), "CUDA") || strings.Contains(strings.ToUpper(candidate.Name), "NVIDIA")
		candidateFree := min(candidate.FreeBytes, candidate.TotalBytes)
		selectedFree := min(selected.FreeBytes, selected.TotalBytes)
		if !available || (nvidia && !selectedNVIDIA) || (nvidia == selectedNVIDIA && candidateFree > selectedFree) {
			selected, available, selectedNVIDIA = candidate, true, nvidia
		}
	}
	return selected, available
}

// ModelDevice returns the persisted compute choice resolved against currently
// usable devices. A missing or stale preference falls back to automatic choice.
func (c *Container) ModelDevice(ctx context.Context) (string, llama.Device, []llama.Device, bool) {
	c.mu.Lock()
	choice := c.modelDevice
	c.mu.Unlock()
	devices := c.LlamaDevices(ctx)
	if choice == "cpu" {
		return "cpu", llama.Device{}, devices, false
	}
	for _, device := range devices {
		if device.ID == choice {
			return choice, device, devices, true
		}
	}
	device, ok := PreferredLlamaDevice(devices)
	if !ok {
		return "cpu", llama.Device{}, devices, false
	}
	return device.ID, device, devices, true
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
	ctx, release := c.OperationContext(ctx)
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
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
	ctx, revision, finish := c.beginModelChange(ctx)
	defer finish()
	p, err := c.startModel(ctx, modelPath)
	if err != nil {
		return err
	}
	return c.commitModel(ctx, revision, p, modelPath, modelID, true)
}

// DownloadModel fetches a catalog model and switches to it. Progress is reported
// under modelmgr.ProgressOp.
func (c *Container) DownloadModel(ctx context.Context, id string, p ports.Progress) error {
	m, ok := modelmgr.Get(id)
	if !ok {
		return fmt.Errorf("app: unknown model %q", id)
	}
	// The user's selection starts at download, not at process startup. A slow
	// download must not acquire a newer revision after a later clear/swap.
	ctx, revision, finish := c.beginModelChange(ctx)
	defer finish()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(c.cfg.DataDir, "models")
	downloader := c.modelDownloader
	if downloader == nil {
		downloader = modelmgr.Download
	}
	path, err := downloader(ctx, m, dir, p)
	if err != nil {
		return err
	}
	parser, err := c.startModel(ctx, path)
	if err != nil {
		return err
	}
	return c.commitModel(ctx, revision, parser, path, id, true)
}

// ClearModel stops llama-server and reverts to the rules parser.
func (c *Container) ClearModel() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("application is closed")
	}
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	prefs.ModelPath, prefs.ModelID = "", ""
	prefs.ModelDisabled = true
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		c.mu.Unlock()
		return err
	}
	c.modelRevision++
	if c.modelCancel != nil {
		c.modelCancel()
		c.modelCancel = nil
	}
	old := c.llama
	c.llama, c.parser = nil, c.rulesParser
	c.modelPath, c.modelID = "", ""
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.log.Info("model cleared; using rules parser")
	return nil
}

// managedParser keeps process ownership separate from parsing capabilities.
// Production uses llama.Parser; tests can exercise swaps without a model/GPU.
type managedParser interface {
	ports.IntentParser
	Close() error
}

func (c *Container) beginModelChange(parent context.Context) (context.Context, uint64, func()) {
	leased, release := c.OperationContext(parent)
	ctx, cancel := context.WithCancel(leased)
	c.mu.Lock()
	if c.modelCancel != nil {
		c.modelCancel()
	}
	c.modelRevision++
	revision := c.modelRevision
	c.modelCancel = cancel
	c.mu.Unlock()
	return ctx, revision, func() {
		c.mu.Lock()
		if c.modelRevision == revision {
			c.modelCancel = nil
		}
		c.mu.Unlock()
		cancel()
		release()
	}
}

func (c *Container) startModel(ctx context.Context, modelPath string) (managedParser, error) {
	probeCtx, probeCancel := context.WithTimeout(ctx, 6*time.Second)
	deviceID, _, _, gpu := c.ModelDevice(probeCtx)
	probeCancel()
	return c.startModelOnDevice(ctx, modelPath, deviceID, gpu)
}

func (c *Container) startModelOnDevice(ctx context.Context, modelPath, deviceID string, gpu bool) (managedParser, error) {
	ctx, cancel := context.WithTimeout(ctx, modelStartTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gpuLayers := c.cfg.AI.GPULayers
	if !gpu {
		gpuLayers, deviceID = -1, ""
	}
	options := llama.Options{
		BinaryPath: c.cfg.AI.LlamaServerPath, Runtimes: c.LlamaRuntimes(),
		ModelPath: modelPath, NCtx: c.cfg.AI.NCtx, NThreads: c.cfg.AI.NThreads,
		GPULayers: gpuLayers, Device: deviceID, StartTimeout: runtimeStartTimeout, Logger: c.log,
	}
	var parser managedParser
	var err error
	if c.modelFactory != nil {
		parser, err = c.modelFactory(ctx, options)
	} else {
		parser, err = llama.New(ctx, options)
	}
	if err == nil && ctx.Err() != nil {
		if parser != nil {
			_ = parser.Close()
		}
		return nil, ctx.Err()
	}
	return parser, err
}

// SetModelDevice persists a CPU or detected GPU choice. When a model is active,
// a replacement is made ready on the new device before the old process stops.
func (c *Container) SetModelDevice(ctx context.Context, choice string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	devices := c.LlamaDevices(probeCtx)
	cancel()
	gpu := false
	if choice != "cpu" {
		for _, device := range devices {
			if device.ID == choice {
				gpu = true
				break
			}
		}
		if !gpu {
			return fmt.Errorf("app: compute device %q is not available", choice)
		}
	}

	c.mu.Lock()
	modelPath, modelID := c.modelPath, c.modelID
	modelStarting := c.modelCancel != nil
	c.mu.Unlock()
	if modelPath == "" && modelStarting {
		modelPath, modelID = c.cfg.AI.ModelPath, c.cfg.AI.ModelID
	}
	if modelPath == "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			return errors.New("application is closed")
		}
		prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
		if err != nil {
			return err
		}
		prefs.ModelDevice = choice
		if err := prefs.Save(c.cfg.DataDir); err != nil {
			return err
		}
		c.modelDevice = choice
		return nil
	}

	changeCtx, revision, finish := c.beginModelChange(ctx)
	defer finish()
	parser, err := c.startModelOnDevice(changeCtx, modelPath, choice, gpu)
	if err != nil {
		return err
	}
	c.mu.Lock()
	err = changeCtx.Err()
	if err == nil && (c.closed || c.modelRevision != revision) {
		err = context.Canceled
	}
	if err == nil {
		prefs, loadErr := config.LoadPrefsChecked(c.cfg.DataDir)
		if loadErr != nil {
			err = loadErr
		} else {
			prefs.ModelDevice = choice
			err = prefs.Save(c.cfg.DataDir)
		}
	}
	if err != nil {
		c.mu.Unlock()
		_ = parser.Close()
		return err
	}
	old := c.llama
	c.llama, c.parser, c.modelDevice = parser, parser, choice
	c.modelPath, c.modelID = modelPath, modelID
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.log.Info("model compute device changed", "device", choice)
	return nil
}

// commitModel is the only publication point for a started parser. Starting a
// replacement is expensive and happens outside the settings lock; the revision
// guard prevents a slow startup from undoing a later clear/swap or shutdown.
func (c *Container) commitModel(ctx context.Context, revision uint64, p managedParser, modelPath, modelID string, persist bool) error {
	c.mu.Lock()
	err := ctx.Err()
	if err == nil && (c.closed || c.modelRevision != revision) {
		err = context.Canceled
	}
	if err == nil && persist {
		var prefs config.Prefs
		prefs, err = config.LoadPrefsChecked(c.cfg.DataDir)
		if err == nil {
			prefs.ModelPath, prefs.ModelID = modelPath, modelID
			prefs.ModelDisabled = false
			err = prefs.Save(c.cfg.DataDir)
		}
	}
	if err != nil {
		c.mu.Unlock()
		if p != nil {
			_ = p.Close()
		}
		return err
	}
	old := c.llama
	c.llama, c.parser = p, p
	c.modelPath, c.modelID = modelPath, modelID
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.log.Info("model ready", "id", modelID)
	return nil
}
