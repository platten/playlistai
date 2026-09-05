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
}

type manifest struct {
	Models []Model `json:"models"`
}

// Filename is the on-disk name a catalog model downloads to.
func (m Model) Filename() string { return m.ID + ".gguf" }

// Verified reports whether the entry carries integrity metadata.
func (m Model) Verified() bool { return m.SHA256 != "" || m.Size > 0 }

var cached []Model

// Catalog returns the embedded model list.
func Catalog() []Model {
	if cached == nil {
		var mf manifest
		if err := json.Unmarshal(manifestJSON, &mf); err != nil {
			panic("modelmgr: bad embedded manifest: " + err.Error())
		}
		cached = mf.Models
	}
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
