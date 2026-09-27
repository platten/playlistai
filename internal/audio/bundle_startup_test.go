package audio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCLAPStartupLeavesBuiltInChecksumToWorker(t *testing.T) {
	if !nativeInferenceAvailable {
		t.Skip("v2 requires native inference")
	}
	manager, dir, m := storedFixtureBundle(t)
	m.Version = 2
	m.Artifacts = m.Artifacts[1:] // v2 uses the built-in worker
	m.ONNXOutputNames = []string{"audio", "text"}
	m.Model.Weights = m.EmbeddingFingerprint()
	writeBundleManifest(t, dir, m)
	if _, _, err := manager.ActiveStartupContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	model := m.File(dir, "audio_model")
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(model, data, 0600); err != nil {
		t.Fatal(err)
	}
	// A same-size replacement passes the layout check, but the child reader
	// rejects it before it can load a native runtime or model.
	if _, _, err := manager.ActiveStartupContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeBundle(dir); err == nil {
		t.Fatal("worker accepted a replaced model")
	}
	if err := os.Remove(model); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ActiveStartupContext(context.Background()); err == nil {
		t.Fatal("startup accepted a missing model")
	}
	if err := os.Symlink(m.File(dir, "text_model"), model); err == nil {
		if _, _, err := manager.ActiveStartupContext(context.Background()); err == nil {
			t.Fatal("startup accepted a model symlink")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := manager.ActiveStartupContext(ctx); err != context.Canceled {
		t.Fatalf("startup cancellation: %v", err)
	}
}

func TestLegacyStartupVerifiesBeforeExecutingBundle(t *testing.T) {
	manager, dir, m := storedFixtureBundle(t)
	if _, _, err := manager.ActiveStartupContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(dir, m.Artifacts[0].Name)
	data, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(worker, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ActiveStartupContext(context.Background()); err == nil {
		t.Fatal("legacy executable passed without checksum verification")
	}
}

func TestMERTStartupLeavesChecksumToWorker(t *testing.T) {
	if !nativeInferenceAvailable {
		t.Skip("MERT requires native inference")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "fixture")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	m := fixtureMERTBundle(t, dir)
	manager := &MERTBundleManager{Directory: parent}
	if err := os.WriteFile(filepath.Join(manager.Directory, "active.json"), []byte(filepath.Base(dir)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ActiveStartupContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	model := m.File(dir, "audio_model")
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(model, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ActiveStartupContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMERTBundle(dir); err == nil {
		t.Fatal("worker accepted a replaced MERT model")
	}
	if err := os.Remove(model); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ActiveStartupContext(context.Background()); err == nil {
		t.Fatal("startup accepted missing MERT model")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := manager.ActiveStartupContext(ctx); err != context.Canceled {
		t.Fatalf("MERT startup cancellation: %v", err)
	}
}
