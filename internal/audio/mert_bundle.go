package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

type MERTParityReport struct {
	ReferenceRevision    string  `json:"referenceRevision"`
	Fixtures             int     `json:"fixtures"`
	MaximumAbsoluteError float64 `json:"maximumAbsoluteError"`
	MinimumCosine        float64 `json:"minimumCosine"`
}

func (p MERTParityReport) Valid() bool {
	return p.ReferenceRevision == MERTRevision && p.Fixtures >= 3 && p.MaximumAbsoluteError >= 0 && p.MaximumAbsoluteError <= 0.0001 && p.MinimumCosine >= 0.9999 && p.MinimumCosine <= 1.000001
}

type MERTBundleManifest struct {
	Version     int                              `json:"version"`
	ID          string                           `json:"id"`
	Label       string                           `json:"label"`
	Platform    string                           `json:"platform"`
	Model       core.AudioRepresentationIdentity `json:"model"`
	MemoryBytes int64                            `json:"memoryBytes"`
	License     string                           `json:"license"`
	SourceURL   string                           `json:"sourceUrl"`
	Parity      MERTParityReport                 `json:"parity"`
	Artifacts   []BundleArtifact                 `json:"artifacts"`
}

func (m MERTBundleManifest) Validate() error {
	if !nativeInferenceAvailable {
		return fmt.Errorf("audio: MERT requires a cgo-enabled desktop build")
	}
	return m.validateRuntime()
}
func (m MERTBundleManifest) validateRuntime() error {
	if m.Version != 1 || !safeName(m.ID) || m.Platform != runtime.GOOS+"/"+runtime.GOARCH || m.Model.Model != "m-a-p/MERT-v1-95M" || m.Model.Revision != MERTRevision || m.Model.Preprocessing != MERTPreprocessingVersion || m.Model.Pooling != MERTPoolingVersion || m.Model.Runtime != "onnxruntime/1.26.0/cpu" || m.Model.Dimension != MERTDimension || m.MemoryBytes <= 0 || m.License == "" || m.SourceURL == "" || !m.Parity.Valid() {
		return fmt.Errorf("audio: incompatible MERT identity, platform, license, or parity")
	}
	names := map[string]bool{"mert-bundle.json": true, "active.json": true, "active.json.tmp": true}
	roles := map[string]bool{}
	for _, a := range m.Artifacts {
		if !safeName(a.Name) || names[a.Name] || roles[a.Role] || a.Size <= 0 || !representationHash(a.SHA256) || a.URL != "" && !strings.HasPrefix(a.URL, "https://") {
			return fmt.Errorf("audio: invalid MERT artifact")
		}
		if a.Role != "audio_model" && a.Role != "runtime" && a.Role != "license" && a.Role != "health" {
			return fmt.Errorf("audio: unknown MERT artifact role")
		}
		if len(a.Data) > 0 && (len(a.Data) > 1<<20 || a.Role != "license" && a.Role != "health") {
			return fmt.Errorf("audio: invalid MERT inline artifact")
		}
		names[a.Name] = true
		roles[a.Role] = true
		if a.ArchiveMember != "" {
			name := filepath.Base(a.ArchiveMember)
			if a.Role != "runtime" || !safeArchiveMember(a.ArchiveMember) || !safeName(name) || names[name] || a.UnpackedSize <= 0 || a.UnpackedSize > 256<<20 || !representationHash(a.UnpackedSHA256) {
				return fmt.Errorf("audio: invalid MERT runtime archive")
			}
			names[name] = true
		}
		if a.Role == "audio_model" && a.SHA256 != m.Model.WeightsSHA256 {
			return fmt.Errorf("audio: MERT weights do not match identity")
		}
	}
	for _, role := range []string{"audio_model", "runtime", "license", "health"} {
		if !roles[role] {
			return fmt.Errorf("audio: missing MERT %s", role)
		}
	}
	return nil
}
func (m MERTBundleManifest) File(dir, role string) string {
	return (BundleManifest{Artifacts: m.Artifacts}).File(dir, role)
}
func (m MERTBundleManifest) DownloadBytes() int64 {
	return (BundleManifest{Artifacts: m.Artifacts}).DownloadBytes()
}
func ReadMERTBundle(dir string) (MERTBundleManifest, error) {
	var m MERTBundleManifest
	f, err := os.Open(filepath.Join(dir, "mert-bundle.json"))
	if err != nil {
		return m, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if err = m.Validate(); err != nil {
		return m, err
	}
	for _, a := range m.Artifacts {
		if !artifactValid(dir, a) {
			return m, fmt.Errorf("audio: MERT artifact integrity failed")
		}
	}
	return m, nil
}

type MERTBundleManager struct {
	Directory   string
	mu          sync.Mutex
	healthCheck func(context.Context, string, MERTBundleManifest) error
}

func (b *MERTBundleManager) Install(ctx context.Context, m MERTBundleManifest, p ports.Progress) (string, error) {
	return b.install(ctx, m, "", p)
}

// InstallLocal imports a maintainer-prepared asset pack using the same checksum
// and actual native parity gate as network installation. Source assets remain.
func (b *MERTBundleManager) InstallLocal(ctx context.Context, source string, p ports.Progress) (string, error) {
	m, err := ReadMERTBundle(source)
	if err != nil {
		return "", err
	}
	return b.install(ctx, m, source, p)
}
func (b *MERTBundleManager) install(ctx context.Context, m MERTBundleManifest, source string, p ports.Progress) (string, error) {
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	var done int64
	for _, a := range m.Artifacts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !downloadValid(dir, a) {
			target := filepath.Join(dir, a.Name)
			switch {
			case source != "":
				if err := copyMERTArtifact(ctx, filepath.Join(source, a.Name), target, a.Size); err != nil {
					return "", err
				}
			case len(a.Data) > 0:
				if err := os.WriteFile(target, a.Data, 0600); err != nil {
					return "", err
				}
			case a.URL != "":
				if _, err := dataset.Download(ctx, a.URL, target, a.Size, a.SHA256, func(n, _ int64) { p.Report("mert-model", done+n, m.DownloadBytes(), "Downloading MERT") }); err != nil {
					return "", err
				}
			default:
				return "", fmt.Errorf("audio: local MERT asset requires importing its prepared bundle")
			}
			if !downloadValid(dir, a) {
				return "", fmt.Errorf("audio: MERT artifact integrity failed")
			}
		}
		if a.ArchiveMember != "" && !artifactValid(dir, a) {
			if err := unpackRuntime(ctx, dir, a); err != nil {
				return "", err
			}
		}
		done += a.Size
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "mert-bundle.json"), raw, 0600); err != nil {
		return "", err
	}
	p.Report("mert-model", done, done, "Checking native MERT parity")
	if b.healthCheck != nil {
		err = b.healthCheck(ctx, dir, m)
	} else {
		w := &MERTWorker{BundleDir: dir, Model: m.Model}
		err = w.Health(ctx)
		_ = w.Close()
	}
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	staged := filepath.Join(b.Directory, "active.json.tmp")
	if err = os.WriteFile(staged, []byte(version), 0600); err != nil {
		return "", err
	}
	if err = os.Rename(staged, filepath.Join(b.Directory, "active.json")); err != nil {
		return "", err
	}
	return dir, nil
}
func copyMERTArtifact(ctx context.Context, source, target string, size int64) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(&contextReader{ctx: ctx, reader: in}, size+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return fmt.Errorf("audio: MERT source size mismatch")
	}
	return nil
}
func (b *MERTBundleManager) Active() (string, MERTBundleManifest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(b.Directory, "active.json"))
	if err != nil {
		return "", MERTBundleManifest{}, err
	}
	name := string(raw)
	if !safeName(name) {
		return "", MERTBundleManifest{}, fmt.Errorf("audio: invalid active MERT bundle")
	}
	dir := filepath.Join(b.Directory, name)
	m, err := ReadMERTBundle(dir)
	return dir, m, err
}
func (b *MERTBundleManager) Remove() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return os.RemoveAll(b.Directory)
}

// MERTParity compares normalized segment embeddings to pinned PyTorch results.
func MERTParity(a, b []float32) bool {
	if !validVector(a, MERTDimension) || !validVector(b, MERTDimension) {
		return false
	}
	var dot, normA, normB float64
	for j := range a {
		if math.Abs(float64(a[j])-float64(b[j])) > 0.0001 {
			return false
		}
		dot += float64(a[j]) * float64(b[j])
		normA += float64(a[j]) * float64(a[j])
		normB += float64(b[j]) * float64(b[j])
	}
	return math.Abs(normA-1) <= 0.0001 && math.Abs(normB-1) <= 0.0001 && dot/math.Sqrt(normA*normB) >= 0.9999
}
