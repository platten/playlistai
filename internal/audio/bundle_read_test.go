package audio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func storedFixtureBundle(t *testing.T) (*BundleManager, string, BundleManifest) {
	t.Helper()
	manager := &BundleManager{Directory: filepath.Join(t.TempDir(), "music-analysis")}
	dir := filepath.Join(manager.Directory, "fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := fixtureBundle("https://example.invalid")
	for _, artifact := range m.Artifacts {
		if err := os.WriteFile(filepath.Join(dir, artifact.Name), []byte("synthetic-bundle-control-fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeBundleManifest(t, dir, m)
	if err := os.WriteFile(filepath.Join(manager.Directory, "active.json"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return manager, dir, m
}

func writeBundleManifest(t *testing.T, dir string, manifest BundleManifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStoredBundleActivationIntegrityAndScopedRemoval(t *testing.T) {
	manager, dir, manifest := storedFixtureBundle(t)
	gotDir, got, err := manager.Active()
	if err != nil || gotDir != dir || got.Model != manifest.Model {
		t.Fatalf("stored bundle activation: %s %+v %v", gotDir, got, err)
	}
	if NativeInferenceAvailable() != nativeInferenceAvailable {
		t.Fatal("native capability does not match the build tag")
	}
	if got.File(dir, "not-present") != "" {
		t.Fatal("missing artifact role invented")
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.Artifacts[0].Name), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Active(); err == nil {
		t.Fatal("corrupt artifact activated")
	}
	sibling := filepath.Join(filepath.Dir(manager.Directory), "audio-analysis.sqlite")
	if err := os.WriteFile(sibling, []byte("retained-derived-evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.Directory); !os.IsNotExist(err) {
		t.Fatalf("managed bundle directory remains: %v", err)
	}
	if raw, err := os.ReadFile(sibling); err != nil || string(raw) != "retained-derived-evidence" {
		t.Fatal("model removal deleted derived evidence", err)
	}
	if _, _, err := manager.Active(); err == nil {
		t.Fatal("removed model remains active")
	}
}

func TestBundleReadersRejectBrokenManifestsAndUnsafePointers(t *testing.T) {
	manager, dir, manifest := storedFixtureBundle(t)
	if err := os.WriteFile(filepath.Join(manager.Directory, "active.json"), []byte("../outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Active(); err == nil {
		t.Fatal("unsafe active pointer followed")
	}
	manifest.Policy = Policy{}
	writeBundleManifest(t, dir, manifest)
	if _, err := ReadRuntimeBundle(dir); err != nil {
		t.Fatal("developer runtime validation unexpectedly requires calibrated policy", err)
	}
	if _, err := ReadBundle(dir); err == nil {
		t.Fatal("desktop bundle accepted an uncalibrated policy")
	}
	manifest.Platform = "invalid-platform"
	writeBundleManifest(t, dir, manifest)
	if _, err := ReadRuntimeBundle(dir); err == nil {
		t.Fatal("incompatible runtime bundle loaded")
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(dir); err == nil {
		t.Fatal("malformed bundle loaded")
	}
	if _, err := ReadBundle(t.TempDir()); err == nil {
		t.Fatal("missing bundle loaded")
	}
}
