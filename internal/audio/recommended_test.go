package audio

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecommendedBundleAndCustomEmbeddingIdentity(t *testing.T) {
	m, err := RecommendedBundle()
	if _, supported := recommendedRuntimes[runtime.GOOS+"/"+runtime.GOARCH]; !supported || !nativeInferenceAvailable {
		if err == nil {
			t.Fatal("unsupported platform offered a recommended runtime")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || m.ID != "clap-music-fp32-v1" || m.Label != "CLAP Music · full precision" || m.Policy.Valid() || m.Model.Dimension != 512 || m.DownloadBytes() < 778209534 {
		t.Fatalf("invalid recommendation: %+v", m)
	}
	if m.Model.Model != "laion/larger_clap_music" || m.Model.Revision != publicCLAPRevision || m.TextUnpadded || m.ONNXOutputNames[0] != "embedding" || m.ONNXOutputNames[1] != "embedding" {
		t.Fatalf("wrong music-only encoder contract: %+v", m)
	}
	if (&Service{Policy: m.Policy}).Ready() {
		t.Fatal("runtime validation promoted to musical calibration")
	}
	for _, mutate := range []func(*BundleManifest){
		func(m *BundleManifest) { m.Artifacts[0].Name = "bundle.json" },
		func(m *BundleManifest) { m.Artifacts[0].Role = "worker" },
		func(m *BundleManifest) { m.TextUnpadded = !m.TextUnpadded },
	} {
		copy := m
		copy.Artifacts = append([]BundleArtifact(nil), m.Artifacts...)
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("unsafe or incompatible custom manifest accepted")
		}
	}
	m.Artifacts[0].SHA256 = m.Artifacts[1].SHA256
	if m.Validate() == nil {
		t.Fatal("changed encoder retained old cache identity")
	}
	m.Model.Weights = m.EmbeddingFingerprint()
	if err := m.Validate(); err != nil {
		t.Fatal("custom pair rejected:", err)
	}
	m.Model.Dimension = 768
	if m.Validate() == nil {
		t.Fatal("incompatible embedding dimension accepted")
	}
}

// Uses real public files only when explicitly requested; ordinary tests never
// download large models. The manager's actual installer and native health run.
func TestRecommendedNativeInstallation(t *testing.T) {
	source := os.Getenv("PLAYLISTAI_TEST_PUBLIC_CLAP")
	if source == "" {
		t.Skip("set PLAYLISTAI_TEST_PUBLIC_CLAP for native public-model validation")
	}
	m, err := RecommendedBundle()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	version := m.ID + "-" + Fingerprint(m)[:16]
	dir := filepath.Join(directory, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range m.Artifacts {
		if len(artifact.Data) > 0 {
			continue
		}
		input := filepath.Join(source, artifact.Name)
		if artifact.Role == "runtime" {
			input = os.Getenv("PLAYLISTAI_TEST_ORT_ARCHIVE")
			if input == "" {
				input = filepath.Join(source, "ort.tgz")
			}
		}
		output := filepath.Join(dir, artifact.Name)
		// Reuse files without copying when possible. A Windows test may read
		// Linux-hosted fixtures over WSL while staging onto its native C: drive.
		if err := os.Link(input, output); err != nil {
			if err := copyNativeFixture(input, output); err != nil {
				t.Fatal(err)
			}
		}
	}
	executable := os.Getenv("PLAYLISTAI_TEST_AUDIO_WORKER")
	if executable == "" {
		executable = filepath.Join(source, "audioworker")
	}
	manager := &BundleManager{Directory: directory, healthCheck: func(ctx context.Context, dir string, m BundleManifest) error {
		worker := &Worker{Executable: executable, BundleDir: dir, Model: m.Model}
		defer worker.Close()
		return worker.Health(ctx)
	}}
	if _, err := manager.Install(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	if _, installed, err := manager.Active(); err != nil || installed.Model != m.Model {
		t.Fatalf("active mismatch: %v", err)
	}
	if desktop := os.Getenv("PLAYLISTAI_TEST_DESKTOP"); desktop != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var input bytes.Buffer
		if err := WriteFrame(&input, WorkerRequest{Protocol: WorkerProtocol, Health: true}); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, desktop, "--audio-worker", dir)
		cmd.Stdin = &input
		// No Python or other executable is discoverable in this child.
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "PYTHONHOME=", "PYTHONPATH=")
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("desktop worker: %v", err)
		}
		var response WorkerResponse
		if err := ReadFrame(bytes.NewReader(output), &response, 16<<20); err != nil {
			t.Fatal(err)
		}
		if response.Protocol != WorkerProtocol || response.Error != "" || response.Model != m.Model {
			t.Fatalf("desktop inference validation failed: %+v", response)
		}
	}
}

func copyNativeFixture(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
