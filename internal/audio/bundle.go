package audio

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

type BundleArtifact struct {
	Role   string `json:"role"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type ParityReport struct {
	ReferenceRevision            string  `json:"referenceRevision"`
	Fixtures                     int     `json:"fixtures"`
	MaximumAbsoluteError         float64 `json:"maximumAbsoluteError"`
	MinimumCosine                float64 `json:"minimumCosine"`
	TokenizerCases               int     `json:"tokenizerCases"`
	TokenizerExact               bool    `json:"tokenizerExact"`
	PreprocessingCases           int     `json:"preprocessingCases"`
	PreprocessingWithinTolerance bool    `json:"preprocessingWithinTolerance"`
}

func (p ParityReport) Valid() bool {
	return p.ReferenceRevision != "" && p.Fixtures >= 3 && p.MaximumAbsoluteError >= 0 && p.MaximumAbsoluteError <= 0.0001 && p.MinimumCosine >= 0.9999 && p.MinimumCosine <= 1.000001 && p.TokenizerCases >= 6 && p.TokenizerExact && p.PreprocessingCases >= 3 && p.PreprocessingWithinTolerance
}

type BundleManifest struct {
	Version     int                     `json:"version"`
	ID          string                  `json:"id"`
	Label       string                  `json:"label"`
	Platform    string                  `json:"platform"`
	Model       core.AudioModelIdentity `json:"model"`
	MemoryBytes int64                   `json:"memoryBytes"`
	License     string                  `json:"license"`
	SourceURL   string                  `json:"sourceUrl"`
	Policy      Policy                  `json:"policy"`
	Parity      ParityReport            `json:"parity"`
	Artifacts   []BundleArtifact        `json:"artifacts"`
}

func (m BundleManifest) Validate() error {
	if !m.Policy.Valid() {
		return fmt.Errorf("audio: bundle needs a policy calibrated on reviewed development data")
	}
	return m.validateRuntime()
}

func (m BundleManifest) validateRuntime() error {
	if m.Version != 1 || !safeName(m.ID) || m.Platform != runtime.GOOS+"/"+runtime.GOARCH || m.Model.Model != "laion/larger_clap_music" || m.Model.Revision == "" || m.Model.Preprocessing != PreprocessingVersion || m.Model.Runtime != "onnxruntime/1.26.0/cpu" || m.Model.Dimension != 512 || m.MemoryBytes <= 0 || m.License == "" || m.SourceURL == "" || !m.Parity.Valid() || m.Parity.ReferenceRevision != m.Model.Revision {
		return fmt.Errorf("audio: bundle requires compatible platform, provenance, CPU runtime, preprocessing, parity, and calibrated policy")
	}
	names, roles := map[string]bool{}, map[string]bool{}
	for _, a := range m.Artifacts {
		hash, err := hex.DecodeString(a.SHA256)
		if !safeName(a.Name) || names[a.Name] || roles[a.Role] || a.Role == "" || a.Size <= 0 || err != nil || len(hash) != 32 || !strings.HasPrefix(a.URL, "https://") {
			return fmt.Errorf("audio: invalid bundle artifact")
		}
		names[a.Name] = true
		roles[a.Role] = true
	}
	for _, role := range []string{"worker", "runtime", "audio_model", "text_model", "vocabulary", "merges", "preprocessing", "license", "health"} {
		if !roles[role] {
			return fmt.Errorf("audio: missing %s artifact", role)
		}
	}
	return nil
}
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, "/\\:")
}
func (m BundleManifest) File(dir, role string) string {
	for _, a := range m.Artifacts {
		if a.Role == role {
			return filepath.Join(dir, a.Name)
		}
	}
	return ""
}
func (m BundleManifest) DownloadBytes() int64 {
	var n int64
	for _, a := range m.Artifacts {
		n += a.Size
	}
	return n
}

type BundleManager struct {
	Directory   string
	mu          sync.Mutex
	healthCheck func(context.Context, string, BundleManifest) error // deterministic installer fixtures only
}

// Install resumes each checksummed artifact and activates only after an actual
// worker health check. A failed installation leaves the previous active bundle.
func (b *BundleManager) Install(ctx context.Context, m BundleManifest, p ports.Progress) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := m.Validate(); err != nil {
		return "", err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	version := m.ID + "-" + Fingerprint(m)[:16]
	dir := filepath.Join(b.Directory, version)
	var done int64
	for _, a := range m.Artifacts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target := filepath.Join(dir, a.Name)
		if !artifactValid(dir, a) {
			base := done
			if _, err := dataset.Download(ctx, a.URL, target, a.Size, a.SHA256, func(n, _ int64) { p.Report("analysis-model", base+n, m.DownloadBytes(), "Downloading music analysis") }); err != nil {
				return "", err
			}
		}
		done += a.Size
	}
	if err := os.Chmod(m.File(dir, "worker"), 0o700); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o600); err != nil {
		return "", err
	}
	p.Report("analysis-model", done, done, "Checking music analysis")
	if b.healthCheck != nil {
		err = b.healthCheck(ctx, dir, m)
	} else {
		worker := &Worker{Executable: m.File(dir, "worker"), BundleDir: dir, Model: m.Model}
		err = worker.Health(ctx)
		_ = worker.Close()
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The pointer is small; an interrupted write never selects a partial bundle.
	staged := filepath.Join(b.Directory, "active.json.tmp")
	if err := os.WriteFile(staged, []byte(version), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(staged, filepath.Join(b.Directory, "active.json")); err != nil {
		return "", err
	}
	p.Report("analysis-model", done, done, "Music analysis ready")
	return dir, nil
}

func artifactValid(dir string, a BundleArtifact) bool {
	ok, _ := dataset.Status(dir, &dataset.Manifest{Files: []dataset.File{{Name: a.Name, Size: a.Size, SHA256: a.SHA256}}})
	return ok
}

func ReadBundle(dir string) (BundleManifest, error) {
	m, err := ReadRuntimeBundle(dir)
	if err != nil {
		return m, err
	}
	return m, m.Validate()
}

// ReadRuntimeBundle permits developer parity checks before policy calibration.
// Desktop installation and activation always call Validate as well.
func ReadRuntimeBundle(dir string) (BundleManifest, error) {
	var m BundleManifest
	raw, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if err := m.validateRuntime(); err != nil {
		return m, err
	}
	for _, a := range m.Artifacts {
		if !artifactValid(dir, a) {
			return m, fmt.Errorf("audio: bundle artifact integrity check failed")
		}
	}
	return m, nil
}

func (b *BundleManager) Active() (string, BundleManifest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(b.Directory, "active.json"))
	if err != nil {
		return "", BundleManifest{}, err
	}
	name := string(raw)
	if !safeName(name) {
		return "", BundleManifest{}, fmt.Errorf("audio: invalid active bundle")
	}
	dir := filepath.Join(b.Directory, name)
	m, err := ReadBundle(dir)
	return dir, m, err
}

func (b *BundleManager) Remove() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Only the manager-owned music-analysis directory is removed. Analysis
	// records live separately and retain their explicit-clear policy.
	return os.RemoveAll(b.Directory)
}
