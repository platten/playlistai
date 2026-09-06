package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/intent/modelmgr"
)

// modelsDir is where DownloadModel puts GGUFs (mirrors app.Container.DownloadModel).
func (a *API) modelsDir() string {
	return filepath.Join(a.app.Config().DataDir, "models")
}

// ModelInfo is one curated catalog entry for the Settings screen.
type ModelInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Params      string `json:"params"`
	Quant       string `json:"quant"`
	SizeApprox  int64  `json:"sizeApprox"`
	LicenseName string `json:"licenseName"`
	LicenseURL  string `json:"licenseUrl"`
	RAMGB       int    `json:"ramGb"`
	Recommended bool   `json:"recommended"`
	// Verified reports whether size + sha256 are pinned in the manifest, so
	// Download checks the file against them rather than trusting the host.
	Verified bool `json:"verified"`
	// Installed reports whether this model's GGUF is already downloaded and
	// valid on disk — the UI shows "Use" instead of "Download", and
	// DownloadModel(id) is a no-op fetch that just switches to it.
	Installed bool `json:"installed"`
}

// ModelHardwareInfo explains how the first-run model list was selected.
type ModelHardwareInfo struct {
	Mode          string `json:"mode"` // "gpu" | "cpu"
	GPUAvailable  bool   `json:"gpuAvailable"`
	GPUName       string `json:"gpuName"`
	VRAMBytes     int64  `json:"vramBytes"`
	VRAMFreeBytes int64  `json:"vramFreeBytes"`
	FitBytes      int64  `json:"fitBytes"`
	ReserveBytes  int64  `json:"reserveBytes"`
}

// ModelRecommendations is the hardware-filtered first-run model list.
type ModelRecommendations struct {
	Models   []ModelInfo       `json:"models"`
	Hardware ModelHardwareInfo `json:"hardware"`
}

// ModelStatus is the active-parser snapshot for the Settings screen.
type ModelStatus struct {
	Backend    string `json:"backend"` // "rules" | "llama"
	Ready      bool   `json:"ready"`
	ModelID    string `json:"modelId"`
	ModelPath  string `json:"modelPath"`
	ModelLabel string `json:"modelLabel"`
}

// LlamaRuntimeInfo tells the UI whether a llama.cpp runtime is installed.
type LlamaRuntimeInfo struct {
	Available bool     `json:"available"`
	Path      string   `json:"path"`
	Kind      string   `json:"kind"`   // "server" | "llama"
	Source    string   `json:"source"` // "staged" | "detected" | "config" | "path" | ...
	Builds    []string `json:"builds"` // ["gpu","cpu"] for an app-staged install
}

// GetLlamaRuntime reports whether llama.cpp (llama-server or the unified
// `llama` binary) is installed and, for an app-staged install, which builds
// are present. When Available is false the model step offers to install it.
func (a *API) GetLlamaRuntime() LlamaRuntimeInfo {
	rt, builds := a.app.LlamaRuntime()
	return LlamaRuntimeInfo{
		Available: rt.Available,
		Path:      rt.Path,
		Kind:      string(rt.Kind),
		Source:    rt.Source,
		Builds:    builds,
	}
}

// InstallLlamaRuntime runs ggml-org's official installer into the app's data
// dir (GPU build when available + a CPU fallback), streaming progress as
// playlistai:progress events under op "llama-install". Blocks until done.
func (a *API) InstallLlamaRuntime() error {
	return a.app.InstallLlamaRuntime(a.context(), NewWailsProgress(), false)
}

// ReinstallLlamaRuntime deletes the existing app-installed llama.cpp and runs
// the installer again. Same progress events as InstallLlamaRuntime.
func (a *API) ReinstallLlamaRuntime() error {
	return a.app.InstallLlamaRuntime(a.context(), NewWailsProgress(), true)
}

// GetModelCatalog returns the built-in list of downloadable models.
func (a *API) GetModelCatalog() []ModelInfo {
	return a.modelInfos(modelmgr.Catalog())
}

// GetModelRecommendations probes the available llama.cpp GPU and returns only
// recommended GGUFs whose weights fit on that device with context/KV headroom.
// Without a usable llama.cpp GPU it returns the two smallest recommended CPU
// choices. Catalog priority is preserved in both modes.
func (a *API) GetModelRecommendations() ModelRecommendations {
	reserve := a.app.ModelVRAMReserve()
	probeCtx, cancel := context.WithTimeout(a.context(), 6*time.Second)
	defer cancel()
	device, gpu := a.app.LlamaHardware(probeCtx)
	fitBytes := device.FreeBytes
	// An active managed model is reclaimable when the user switches models.
	// Add its file size back without ever exceeding the device's total VRAM.
	if gpu {
		if activePath, _ := a.app.CurrentModel(); activePath != "" {
			if stat, err := os.Stat(activePath); err == nil {
				fitBytes = min(device.TotalBytes, fitBytes+stat.Size())
			}
		}
	}
	hw := modelmgr.Hardware{GPUAvailable: gpu, AvailableVRAMBytes: fitBytes, ReserveBytes: reserve}
	mode := "cpu"
	if gpu {
		mode = "gpu"
	}
	return ModelRecommendations{
		Models: a.modelInfos(modelmgr.Recommendations(modelmgr.Catalog(), hw)),
		Hardware: ModelHardwareInfo{
			Mode: mode, GPUAvailable: gpu, GPUName: device.Name,
			VRAMBytes: device.TotalBytes, VRAMFreeBytes: device.FreeBytes,
			FitBytes: fitBytes, ReserveBytes: reserve,
		},
	}
}

func (a *API) modelInfos(src []modelmgr.Model) []ModelInfo {
	dir := a.modelsDir()
	out := make([]ModelInfo, 0, len(src))
	for _, m := range src {
		out = append(out, ModelInfo{
			ID: m.ID, Label: m.Label, Params: m.Params, Quant: m.Quant,
			SizeApprox: m.SizeApprox, LicenseName: m.LicenseName, LicenseURL: m.LicenseURL,
			RAMGB: m.RAMGB, Recommended: m.Recommended, Verified: m.Verified(),
			Installed: modelmgr.IsInstalled(m, dir),
		})
	}
	return out
}

// InstalledModel is a GGUF already present on disk.
type InstalledModel struct {
	Path      string `json:"path"`
	Name      string `json:"name"`      // file basename
	SizeBytes int64  `json:"sizeBytes"` // 0 if unknown
	Label     string `json:"label"`     // catalog label, or "" for a stray/custom file
	CatalogID string `json:"catalogId"` // "" when it's not one of the curated models
	Active    bool   `json:"active"`    // currently the running model
}

// GetInstalledModels lists every valid .gguf found in the config data dir and
// its models/ subdir. The wizard offers each with a "Use" button so a model
// that's already downloaded isn't fetched again. Catalog models also appear
// here (with Label/CatalogID set) but the wizard already covers those via
// GetModelCatalog().installed — the wizard shows only the ones with no
// CatalogID.
func (a *API) GetInstalledModels() []InstalledModel {
	activeModelPath, _ := a.app.CurrentModel()

	seen := map[string]bool{}
	var out []InstalledModel
	for _, dir := range []string{a.modelsDir(), a.app.Config().DataDir} {
		for _, p := range modelmgr.Installed(dir) {
			abs := p
			if x, err := filepath.Abs(p); err == nil {
				abs = x
			}
			if seen[abs] || modelmgr.ValidateGGUF(p) != nil {
				continue
			}
			seen[abs] = true

			var size int64
			if fi, err := os.Stat(p); err == nil {
				size = fi.Size()
			}
			im := InstalledModel{Path: p, Name: filepath.Base(p), SizeBytes: size}
			id := strings.TrimSuffix(im.Name, ".gguf")
			if m, ok := modelmgr.Get(id); ok {
				im.CatalogID, im.Label = m.ID, m.Label
			}
			if activeModelPath != "" {
				if ax, err := filepath.Abs(activeModelPath); err == nil {
					im.Active = ax == abs
				}
			}
			out = append(out, im)
		}
	}
	return out
}

// GetModelStatus reports which parser is active and, if it's the local model,
// which one.
func (a *API) GetModelStatus() ModelStatus {
	info := a.app.IntentParser().Info()
	path, id := a.app.CurrentModel()

	label := ""
	if m, ok := modelmgr.Get(id); ok {
		label = m.Label
	} else if path != "" {
		label = filepath.Base(path)
	}

	return ModelStatus{
		Backend:    info.Backend,
		Ready:      info.Ready,
		ModelID:    id,
		ModelPath:  path,
		ModelLabel: label,
	}
}

// DownloadModel fetches a catalog model (progress under op "model") and switches
// to it. Blocks until the model is downloaded and llama-server is healthy.
func (a *API) DownloadModel(id string) error {
	return a.app.DownloadModel(a.context(), id, NewWailsProgress())
}

// UseModelFile points the local parser at a GGUF the user already has.
func (a *API) UseModelFile(path string) error {
	return a.app.SetModel(a.context(), path, "")
}

// ClearModel stops the local model and reverts to the rules parser.
func (a *API) ClearModel() error {
	return a.app.ClearModel()
}
