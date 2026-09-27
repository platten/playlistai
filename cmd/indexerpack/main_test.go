package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/indexerbundle"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestOfflineValidationRequiresEveryCPUAndCUDABundle(t *testing.T) {
	err := validateOfflineModels("cpu-mert", "", "cpu-clap", "cuda-clap")
	if err == nil || !strings.Contains(err.Error(), "CUDA MERT") {
		t.Fatalf("missing CUDA MERT bundle error = %v", err)
	}
}

func TestExtractCUDAFromExecutable(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("the offline CUDA indexer is built for linux/amd64")
	}
	root := t.TempDir()
	source := filepath.Join(root, "previous-offline")
	files := map[string][]byte{}
	manifest := audio.MERTBundleManifest{
		Version: 1, ID: "mert-test-cuda", Platform: "linux/amd64", MemoryBytes: 1,
		License: "CC-BY-NC-4.0", SourceURL: "https://huggingface.co/m-a-p/MERT-v1-95M",
		Model: core.AudioRepresentationIdentity{
			Model: "m-a-p/MERT-v1-95M", Revision: audio.MERTRevision,
			Preprocessing: audio.MERTPreprocessingVersion, Pooling: audio.MERTPoolingVersion,
			Runtime: "onnxruntime/1.26.0/cuda", Dimension: audio.MERTDimension,
		},
		Parity: audio.MERTParityReport{ReferenceRevision: audio.MERTRevision, Fixtures: 3, MaximumAbsoluteError: 0.0026, MinimumCosine: 0.99991},
	}
	for _, item := range []struct{ role, name string }{
		{"audio_model", "mert-audio.onnx"}, {"health", "health.json"}, {"license", "LICENSES.txt"},
		{"runtime", "libonnxruntime.so"}, {"runtime_dependency_providers_shared", "libonnxruntime_providers_shared.so"},
		{"runtime_dependency_providers_cuda", "libonnxruntime_providers_cuda.so"}, {"runtime_dependency_cuda_00", "libcudart.so.12"},
	} {
		data := []byte("fixture " + item.role)
		files[item.name] = data
		hash := sha256.Sum256(data)
		artifact := audio.BundleArtifact{Role: item.role, Name: item.name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
		manifest.Artifacts = append(manifest.Artifacts, artifact)
		if item.role == "audio_model" {
			manifest.Model.WeightsSHA256 = artifact.SHA256
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files["mert-bundle.json"] = raw
	writeSource := func(corrupt bool) {
		t.Helper()
		file, err := os.Create(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("launcher"); err != nil {
			t.Fatal(err)
		}
		archive := zip.NewWriter(file)
		for name, data := range files {
			entry, err := archive.Create("mert/cuda/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if corrupt && name == "libcudart.so.12" {
				data = []byte("corrupt")
			}
			if _, err := entry.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		end, err := file.Seek(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(indexerbundle.Trailer(uint64(end - int64(len("launcher"))))); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}

	writeSource(false)
	out := filepath.Join(root, "mert-cuda")
	if err := extractCUDAFromExecutable(source, out); err != nil {
		t.Fatal(err)
	}
	if got, err := audio.ReadMERTBundle(out); err != nil || got.Backend() != "cuda" {
		t.Fatalf("extracted CUDA bundle: backend=%q err=%v", got.Backend(), err)
	}
	if err := extractCUDAFromExecutable(source, out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing destination error = %v", err)
	}
	writeSource(true)
	corruptOut := filepath.Join(root, "corrupt-cuda")
	if err := extractCUDAFromExecutable(source, corruptOut); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("corrupt CUDA source error = %v", err)
	}
	if _, err := os.Stat(corruptOut); !os.IsNotExist(err) {
		t.Fatalf("corrupt source published destination: %v", err)
	}
}

func TestOfflineValidationRejectsUnavailableBundlePaths(t *testing.T) {
	err := validateOfflineModels("missing-cpu-mert", "missing-cuda-mert", "missing-cpu-clap", "missing-cuda-clap")
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable bundle error = %v", err)
	}
}

func TestValidatePackagedExecutableRequiresCodecPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-indexer-offline")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("launcher"); err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	entry, err := zw.Create("mert/cpu/mert-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	end, err := file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(indexerbundle.Trailer(uint64(end - int64(len("launcher"))))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = validatePackagedExecutable(path, false)
	if err == nil || !strings.Contains(err.Error(), "missing codec payload") {
		t.Fatalf("missing codec payload error = %v", err)
	}
}

func TestValidatePackagedExecutableAcceptsVerifiedCodecPayload(t *testing.T) {
	root := t.TempDir()
	codec := filepath.Join(root, "codec")
	if err := os.Mkdir(codec, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"ffmpeg": []byte("one"), "ffprobe": []byte("two"), "LICENSES.txt": []byte("three"), "build-info.txt": []byte("four")}
	manifest := localaudio.Manifest{
		SchemaVersion: 2, ID: "ffmpeg-8.1.2-chromaprint-1.6.1-test-v2", Platform: runtime.GOOS + "/" + runtime.GOARCH,
		FFmpegVersion: "8.1.2", SourceURL: "https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz", SourceSHA256: "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c",
		ChromaprintVersion: "1.6.1", ChromaprintSourceURL: "https://github.com/acoustid/chromaprint/archive/refs/tags/v1.6.1.tar.gz", ChromaprintSourceSHA256: "7065ec9db48ac1fa929ec6c42afcd966605b1bfe48b6d5e64c25378a05f4fb02", ChromaprintLicense: "MIT",
		License: "LGPL-2.1-or-later", NetworkDisabled: true, EnabledProtocols: []string{"file", "pipe"}, EnabledDemuxers: []string{"flac", "mp3", "aac", "mov", "wav"}, EnabledDecoders: []string{"flac", "mp3float", "aac", "pcm_f32le"}, EnabledMuxers: []string{"pcm_f32le", "chromaprint"},
	}
	for name, contents := range files {
		mode := os.FileMode(0o644)
		role := map[string]string{"ffmpeg": "ffmpeg", "ffprobe": "ffprobe", "LICENSES.txt": "licenses", "build-info.txt": "build_info"}[name]
		executable := role == "ffmpeg" || role == "ffprobe"
		if executable {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(codec, name), contents, mode); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(contents)
		manifest.Artifacts = append(manifest.Artifacts, localaudio.Artifact{Name: name, Role: role, Size: int64(len(contents)), SHA256: hex.EncodeToString(hash[:]), Executable: executable})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codec, localaudio.ManifestName), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "launcher")
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "playlist-indexer")
	if err := pack(launcher, codec, "", "", "", "", out); err != nil {
		t.Fatal(err)
	}
	if err := validatePackagedExecutable(out, false); err != nil {
		t.Fatal(err)
	}
}
