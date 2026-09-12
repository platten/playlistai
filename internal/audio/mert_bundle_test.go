package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureMERTBundle(t *testing.T, dir string) MERTBundleManifest {
	t.Helper()
	m := MERTBundleManifest{Version: 1, ID: "mert-test", Platform: runtime.GOOS + "/" + runtime.GOARCH, Model: mertTestModel(), MemoryBytes: 1, License: "CC-BY-NC-4.0", SourceURL: "https://huggingface.co/m-a-p/MERT-v1-95M", Parity: MERTParityReport{ReferenceRevision: MERTRevision, Fixtures: 3, MinimumCosine: 1}}
	for _, role := range []string{"audio_model", "runtime", "license", "health"} {
		data := []byte("synthetic " + role)
		hash := sha256.Sum256(data)
		a := BundleArtifact{Role: role, Name: role + ".fixture", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
		if role == "audio_model" {
			m.Model.WeightsSHA256 = a.SHA256
		}
		m.Artifacts = append(m.Artifacts, a)
		if dir != "" {
			if err := os.WriteFile(filepath.Join(dir, a.Name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for role, name := range MERTWindowsRuntimeDependencies(m.Platform) {
		data := []byte("synthetic " + role)
		hash := sha256.Sum256(data)
		m.Artifacts = append(m.Artifacts, BundleArtifact{Role: role, Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
		if dir != "" {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if dir != "" {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "mert-bundle.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return m
}
func TestMERTBundleRejectsMismatch(t *testing.T) {
	if err := fixtureMERTBundle(t, "").validateRuntime(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*MERTBundleManifest){
		func(m *MERTBundleManifest) { m.Model.WeightsSHA256 = "" }, func(m *MERTBundleManifest) { m.Model.Preprocessing = PreprocessingVersion }, func(m *MERTBundleManifest) { m.Model.Pooling = "other" }, func(m *MERTBundleManifest) { m.Parity.Fixtures = 2 }, func(m *MERTBundleManifest) { m.Parity.MaximumAbsoluteError = math.NaN() }, func(m *MERTBundleManifest) { m.Parity.MinimumCosine = 0.9 }, func(m *MERTBundleManifest) { m.Artifacts[0].Name = "../model" }, func(m *MERTBundleManifest) { m.Artifacts[0].Role = "text_model" }, func(m *MERTBundleManifest) { m.Artifacts[1].Name = m.Artifacts[0].Name }, func(m *MERTBundleManifest) { m.Artifacts[1].SHA256 = "00" }, func(m *MERTBundleManifest) { m.Artifacts[1].ArchiveMember = "../runtime" }, func(m *MERTBundleManifest) { m.Platform = "other" },
	} {
		m := fixtureMERTBundle(t, "")
		change(&m)
		if m.validateRuntime() == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
func TestMERTLocalInstallHealthAndIntegrity(t *testing.T) {
	if !nativeInferenceAvailable {
		t.Skip("native installation requires CGO; runtime-independent manifest tests run separately")
	}
	source := t.TempDir()
	m := fixtureMERTBundle(t, source)
	manager := &MERTBundleManager{Directory: filepath.Join(t.TempDir(), "managed"), healthCheck: func(context.Context, string, MERTBundleManifest) error { return nil }}
	installed, err := manager.InstallLocal(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	active, got, err := manager.Active()
	if err != nil || active != installed || got.Model != m.Model {
		t.Fatal("activation mismatch", err)
	}
	if err = os.WriteFile(filepath.Join(source, m.Artifacts[0].Name), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.InstallLocal(context.Background(), source, nil); err == nil {
		t.Fatal("corrupt input installed")
	}
	m = fixtureMERTBundle(t, source)
	m.ID = "another-version"
	raw, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "mert-bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager.healthCheck = func(context.Context, string, MERTBundleManifest) error { return errors.New("parity failed") }
	if _, err = manager.InstallLocal(context.Background(), source, nil); err == nil {
		t.Fatal("health gate bypassed")
	}
	active, _, err = manager.Active()
	if err != nil || active != installed {
		t.Fatal("failed install replaced active", err)
	}
	if err = manager.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("source assets removed", err)
	}
}
func TestMERTNoCGOGate(t *testing.T) {
	if !nativeInferenceAvailable && fixtureMERTBundle(t, "").Validate() == nil {
		t.Fatal("no-CGO install accepted")
	}
}

func TestWindowsMERTBundleRequiresEveryAppLocalDependency(t *testing.T) {
	for platform, count := range map[string]int{"windows/amd64": 4, "windows/arm64": 3, "linux/amd64": 0} {
		if got := len(MERTWindowsRuntimeDependencies(platform)); got != count {
			t.Fatalf("%s dependency count%d", platform, got)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	m := fixtureMERTBundle(t, "")
	for i, a := range m.Artifacts {
		if strings.HasPrefix(a.Role, "runtime_dependency_") {
			broken := m
			broken.Artifacts = append([]BundleArtifact(nil), m.Artifacts[:i]...)
			broken.Artifacts = append(broken.Artifacts, m.Artifacts[i+1:]...)
			if broken.validateRuntime() == nil {
				t.Fatal("missing app-local CRT accepted", a.Role)
			}
		}
	}
}
