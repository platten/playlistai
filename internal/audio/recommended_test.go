package audio

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRecommendedBundleAndCustomEmbeddingIdentity(t *testing.T) {
	m, err := RecommendedBundle()
	platform := runtime.GOOS + "/" + runtime.GOARCH
	runtimeDownload, supported := recommendedRuntimes[platform]
	if !supported || !nativeInferenceAvailable {
		if err == nil {
			t.Fatal("unsupported platform offered a recommended runtime")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || m.ID != "custom-clap-cpu-v2" || m.Label != "LAION original HTSAT-base music checkpoint · CPU" || m.Policy.Valid() || m.Model.Dimension != 512 || m.DownloadBytes() != 784350703+runtimeDownload.UnpackedSize {
		t.Fatalf("invalid recommendation: %+v", m)
	}
	if m.Model.Model != "LAION original HTSAT-base music checkpoint" || m.Model.Revision != originalCLAPRevision || m.Model.Weights != "f208f3bff6cfd4dee3fc5a274168e7db463842246e1c7a88755a8a80bbb6bc9d" || m.TextUnpadded || m.ONNXOutputNames[0] != "embedding" || m.ONNXOutputNames[1] != "embedding" {
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
	for i := range m.Artifacts {
		if m.Artifacts[i].Role == "audio_model" {
			m.Artifacts[i].SHA256 = m.Artifacts[0].SHA256
		}
	}
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

func TestRecommendedBundleCoversPublishedPlatforms(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"darwin/arm64", "linux/arm64", "linux/amd64", "windows/arm64", "windows/amd64"} {
		m, err := recommendedBundle(platform)
		if err != nil {
			t.Fatal(platform, err)
		}
		runtimeDownload := recommendedRuntimes[platform]
		if m.Platform != platform || m.Artifacts[0].Name != filepath.Base(runtimeDownload.ArchiveMember) || m.Artifacts[0].Size != runtimeDownload.UnpackedSize || m.Artifacts[0].SHA256 != runtimeDownload.UnpackedSHA256 {
			t.Fatalf("%s selected wrong runtime: %+v", platform, m.Artifacts[0])
		}
		wantURL := "/clap-" + strings.ReplaceAll(platform, "/", "-") + "/manifest.json"
		if !strings.HasSuffix(m.Artifacts[0].URL, wantURL) || m.Model.Weights != "f208f3bff6cfd4dee3fc5a274168e7db463842246e1c7a88755a8a80bbb6bc9d" {
			t.Fatalf("%s selected wrong pack or embedding identity: %+v", platform, m)
		}
	}
	if _, err := recommendedBundle("darwin/amd64"); err == nil {
		t.Fatal("unpublished macOS Intel runtime accepted")
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
	executable := os.Getenv("PLAYLISTAI_TEST_AUDIO_WORKER")
	if executable == "" {
		executable = filepath.Join(source, "audioworker")
	}
	manager := &BundleManager{Directory: directory, healthCheck: func(ctx context.Context, dir string, m BundleManifest) error {
		worker := &Worker{Executable: executable, BundleDir: dir, Model: m.Model}
		defer worker.Close()
		return worker.Health(ctx)
	}}
	dir, err := manager.InstallDirectory(context.Background(), source, nil)
	if err != nil {
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
