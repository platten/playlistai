package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

func managedCLAPFixture(t *testing.T, root, backend string) {
	t.Helper()
	dir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := audio.RecommendedBundle()
	if err != nil {
		t.Fatal(err)
	}
	m.ID, m.Model.Runtime = "fixture-"+backend, "onnxruntime/1.26.0/"+backend
	for index := range m.Artifacts {
		a := &m.Artifacts[index]
		data := []byte(a.Role)
		digest := sha256.Sum256(data)
		a.Size, a.SHA256, a.Data = int64(len(data)), hex.EncodeToString(digest[:]), nil
		if err := os.WriteFile(filepath.Join(dir, a.Name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.Model.Weights = m.EmbeddingFingerprint()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active.json"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func managedMERTFixture(t *testing.T, root, backend string) {
	t.Helper()
	dir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := audio.MERTBundleManifest{
		Version: 1, ID: "fixture-" + backend, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Model:       core.AudioRepresentationIdentity{Model: "m-a-p/MERT-v1-95M", Revision: audio.MERTRevision, Preprocessing: audio.MERTPreprocessingVersion, Pooling: audio.MERTPoolingVersion, Runtime: "onnxruntime/1.26.0/" + backend, Dimension: audio.MERTDimension},
		MemoryBytes: 1, License: "fixture", SourceURL: "https://example.invalid", Parity: audio.MERTParityReport{ReferenceRevision: audio.MERTRevision, Fixtures: 3, MinimumCosine: 1},
	}
	roles := []string{"audio_model", "runtime", "license", "health"}
	if backend == "cuda" {
		roles = append(roles, "runtime_dependency_providers_shared", "runtime_dependency_providers_cuda")
	}
	for role := range audio.MERTWindowsRuntimeDependencies(m.Platform) {
		roles = append(roles, role)
	}
	for _, role := range roles {
		data := []byte(role)
		digest := sha256.Sum256(data)
		name := role + ".bin"
		if required := audio.MERTWindowsRuntimeDependencies(m.Platform)[role]; required != "" {
			name = required
		}
		m.Artifacts = append(m.Artifacts, audio.BundleArtifact{Role: role, Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])})
		if role == "audio_model" {
			m.Model.WeightsSHA256 = hex.EncodeToString(digest[:])
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mert-bundle.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active.json"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledModelSlotsPreferCUDAAndFallBackToCPU(t *testing.T) {
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" && runtime.GOOS != "windows" || !audio.NativeInferenceAvailable() {
		t.Skip("CUDA model bundles require a native Linux or Windows x64 worker")
	}
	ctx := context.Background()
	c, err := New(ctx, testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	managedCLAPFixture(t, c.analysis.bundles.Directory, "cpu")
	managedCLAPFixture(t, c.analysis.alternate.Directory, "cuda")
	managedMERTFixture(t, c.enhanced.bundles.Directory, "cpu")
	managedMERTFixture(t, c.enhanced.alternate.Directory, "cuda")
	if manager, err := c.analysis.managerFor(ctx, "cpu"); err != nil || manager != c.analysis.bundles {
		t.Fatalf("CPU CLAP slot: %v", err)
	}
	if manager, err := c.enhanced.managerFor(ctx, "cuda"); err != nil || manager != c.enhanced.alternate {
		t.Fatalf("CUDA MERT slot: %v", err)
	}
	if got := c.analysis.installedBundles(ctx, false); len(got) != 1 || got[0].manifest.Backend() != "cpu" {
		t.Fatalf("CPU-only CLAP selection: %+v", got)
	}
	if got := c.enhanced.installedBundles(ctx, false); len(got) != 1 || got[0].manifest.Backend() != "cpu" {
		t.Fatalf("CPU-only MERT selection: %+v", got)
	}
	if got := c.analysis.installedBundles(ctx, true); len(got) != 2 || got[0].manifest.Backend() != "cuda" || got[1].manifest.Backend() != "cpu" {
		t.Fatalf("CUDA CLAP preference: %+v", got)
	}
	if got := c.enhanced.installedBundles(ctx, true); len(got) != 2 || got[0].manifest.Backend() != "cuda" || got[1].manifest.Backend() != "cpu" {
		t.Fatalf("CUDA MERT preference: %+v", got)
	}
	triedCLAP := []string{}
	c.analysis.healthCheck = func(_ context.Context, worker *audio.Worker) error {
		triedCLAP = append(triedCLAP, worker.EffectiveDevice())
		if worker.EffectiveDevice() == "cuda:0" {
			return errors.New("CUDA unavailable")
		}
		return nil
	}
	if err := c.loadAnalysis(ctx); err != nil || c.analysis.manifest.Backend() != "cpu" || len(triedCLAP) != 2 || triedCLAP[0] != "cuda:0" || triedCLAP[1] != "cpu" {
		t.Fatalf("CLAP runtime fallback: %v, tried=%v", err, triedCLAP)
	}
	triedMERT := []string{}
	c.enhanced.healthCheck = func(_ context.Context, worker *audio.MERTWorker) error {
		triedMERT = append(triedMERT, worker.EffectiveDevice())
		if worker.EffectiveDevice() == "cuda:0" {
			return errors.New("CUDA unavailable")
		}
		return nil
	}
	if err := c.loadMERT(ctx); err != nil || c.enhanced.manifest.Backend() != "cpu" || len(triedMERT) != 2 || triedMERT[0] != "cuda:0" || triedMERT[1] != "cpu" {
		t.Fatalf("MERT runtime fallback: %v, tried=%v", err, triedMERT)
	}
}
