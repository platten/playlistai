// Package modelmgr manages GGUF models for the local intent parser: a curated,
// embedded catalog, a resumable download (reusing internal/dataset), a basic
// "is this a GGUF" check for user-supplied files, and a listing of what is
// installed.
package modelmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "embed"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

//go:embed models-manifest.json
var manifestJSON []byte

// ProgressOp is the op label for download progress reports.
const ProgressOp = "model"

// Model is one curated catalog entry.
type Model struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Params      string `json:"params"`
	Quant       string `json:"quant"`
	SizeApprox  int64  `json:"size_approx"` // display only
	Size        int64  `json:"size"`        // 0 → no size check on download
	SHA256      string `json:"sha256"`      // "" → no checksum on download
	URL         string `json:"url"`
	LicenseName string `json:"license_name"`
	LicenseURL  string `json:"license_url"`
	RAMGB       int    `json:"ram_gb"`
	Recommended bool   `json:"recommended"`
	// BestForVRAMGB identifies the preferred intent model at nominal NVIDIA
	// VRAM tiers. Eligibility is still checked against actual free bytes.
	BestForVRAMGB []int `json:"best_for_vram_gb,omitempty"`
}

type manifest struct {
	Models []Model `json:"models"`
}

// Hardware describes the device capacity relevant to model recommendations.
// A GPU is considered available only when the selected llama.cpp runtime can
// enumerate it; a GPU that llama.cpp cannot use must not influence the list.
type Hardware struct {
	GPUAvailable       bool
	TotalVRAMBytes     int64
	AvailableVRAMBytes int64
	ReserveBytes       int64
}

// Filename is the on-disk name a catalog model downloads to.
func (m Model) Filename() string { return m.ID + ".gguf" }

// Verified reports whether the entry carries integrity metadata.
func (m Model) Verified() bool { return m.SHA256 != "" || m.Size > 0 }

// BestForVRAM reports whether m is the preferred intent model for tierGB.
func (m Model) BestForVRAM(tierGB int) bool {
	for _, tier := range m.BestForVRAMGB {
		if tier == tierGB {
			return true
		}
	}
	return false
}

// VRAMTierGB maps llama.cpp's binary byte count to a nominal GPU tier. The
// half-GiB rounding allowance accounts for small firmware/display reservations
// in device totals such as 8123 MiB on an advertised 8 GB GPU. Tiers are capped
// at 32 because that is the largest currently tested consumer profile.
func VRAMTierGB(totalBytes int64) int {
	if totalBytes <= 0 {
		return 0
	}
	roundedGB := int((totalBytes + (1 << 29)) >> 30)
	for _, tier := range []int{32, 24, 16, 12, 8, 4} {
		if roundedGB >= tier {
			return tier
		}
	}
	return roundedGB
}

var (
	cached     []Model
	cachedOnce sync.Once
)

// Catalog returns the embedded model list.
func Catalog() []Model {
	cachedOnce.Do(func() {
		var mf manifest
		if err := json.Unmarshal(manifestJSON, &mf); err != nil {
			panic("modelmgr: bad embedded manifest: " + err.Error())
		}
		cached = mf.Models
	})
	return cached
}

// Get looks up a catalog model by id.
func Get(id string) (Model, bool) {
	for _, m := range Catalog() {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func modelBytes(model Model) int64 {
	if model.Size > 0 {
		return model.Size
	}
	return model.SizeApprox
}

func smallestModel(models []Model) Model {
	var smallest Model
	for _, model := range models {
		if modelBytes(model) > 0 && (smallest.ID == "" || modelBytes(model) < modelBytes(smallest)) {
			smallest = model
		}
	}
	return smallest
}

func gpuModelCapacity(hw Hardware) int64 {
	available := hw.AvailableVRAMBytes
	if hw.TotalVRAMBytes > 0 {
		available = min(available, hw.TotalVRAMBytes)
	}
	return max(0, available-max(0, hw.ReserveBytes))
}

// Recommendations selects the largest curated GPU model whose complete weights
// fit in one device's available memory with runtime headroom. CPU mode selects
// only the smallest catalog model, independently of static priority badges.
func Recommendations(models []Model, hw Hardware) []Model {
	if !hw.GPUAvailable {
		model := smallestModel(models)
		if model.ID == "" {
			return nil
		}
		model.Recommended = true
		return []Model{model}
	}
	capacity := gpuModelCapacity(hw)

	eligible := make([]Model, 0, len(models))
	for _, model := range models {
		if !model.Recommended || modelBytes(model) <= 0 {
			continue
		}
		if modelBytes(model) > capacity {
			continue
		}
		eligible = append(eligible, model)
	}
	if len(eligible) == 0 {
		return nil
	}
	largest := eligible[0]
	for _, model := range eligible[1:] {
		if modelBytes(model) > modelBytes(largest) {
			largest = model
		}
	}
	return []Model{largest}
}

// WizardModels includes the smallest catalog download alongside the single
// hardware-selected recommendation. Listing the smaller alternative does not
// imply it fits in GPU memory; only a fitting choice receives the badge.
func WizardModels(models []Model, hw Hardware) []Model {
	choices := Recommendations(models, hw)
	smallest := smallestModel(models)
	if smallest.ID == "" || (len(choices) > 0 && choices[0].ID == smallest.ID) {
		return choices
	}
	smallest.Recommended = len(choices) == 0 && (!hw.GPUAvailable ||
		modelBytes(smallest) <= gpuModelCapacity(hw))
	return append(choices, smallest)
}

// CatalogForHardware retains every manual choice, but marks only the same
// hardware-selected recommendation offered in setup. Never mutate the catalog.
func CatalogForHardware(models []Model, hw Hardware) []Model {
	recommended := ""
	for _, model := range WizardModels(models, hw) {
		if model.Recommended {
			recommended = model.ID
		}
	}
	out := append([]Model(nil), models...)
	for i := range out {
		out[i].Recommended = out[i].ID == recommended
	}
	return out
}

// Download fetches a catalog model into destDir/<id>.gguf, resuming a partial
// download and verifying size/sha256 when the entry provides them. Progress is
// reported in bytes under ProgressOp. Returns the model path.
//
// If destDir/<id>.gguf is already present and passes the same checks
// (size when pinned, sha256 when pinned, GGUF magic always), it is returned
// as-is with no network request — re-selecting an already-downloaded model
// must not re-download it.
func Download(ctx context.Context, m Model, destDir string, p ports.Progress) (string, error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	if m.URL == "" {
		return "", fmt.Errorf("modelmgr: model %q has no URL", m.ID)
	}
	target := filepath.Join(destDir, m.Filename())

	if IsInstalled(m, destDir) {
		p.Report(ProgressOp, m.Size, m.Size, "ready")
		return target, nil
	}

	p.Report(ProgressOp, 0, m.Size, "downloading "+m.Label)
	if _, err := dataset.Download(ctx, m.URL, target, m.Size, m.SHA256, func(done, total int64) {
		p.Report(ProgressOp, done, total, m.Label)
	}); err != nil {
		return "", err
	}

	if err := ValidateGGUF(target); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	p.Report(ProgressOp, m.Size, m.Size, "ready")
	return target, nil
}

// ValidateGGUF checks that path exists, is non-empty, and starts with the GGUF
// magic bytes. It does not validate the model's contents.
func ValidateGGUF(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("modelmgr: %w", err)
	}
	if fi.IsDir() || fi.Size() < 8 {
		return fmt.Errorf("modelmgr: %s is not a model file", path)
	}
	f, err := os.Open(path) //nolint:gosec // operator-chosen path
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return err
	}
	if string(magic) != "GGUF" {
		return fmt.Errorf("modelmgr: %s is not a GGUF file (bad magic)", path)
	}
	return nil
}

// IsInstalled reports whether m's GGUF is already downloaded into destDir and
// passes m's integrity checks (exact size when the manifest pins one, sha256
// when it pins one, GGUF magic always). Used to skip a redundant download and
// to label the model in the UI.
func IsInstalled(m Model, destDir string) bool {
	target := filepath.Join(destDir, m.Filename())
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() || fi.Size() < 8 {
		return false
	}
	if m.Size > 0 && fi.Size() != m.Size {
		return false
	}
	if ValidateGGUF(target) != nil {
		return false
	}
	if m.SHA256 != "" && !fileHasSHA256(target, m.SHA256) {
		return false
	}
	return true
}

func fileHasSHA256(path, want string) bool {
	f, err := os.Open(path) //nolint:gosec // manifest-derived path
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want)
}

// Installed lists the .gguf files present in destDir.
func Installed(destDir string) []string {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".gguf") {
			out = append(out, filepath.Join(destDir, e.Name()))
		}
	}
	return out
}
