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

	"github.com/platten/playlistai/internal/indexerbundle"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestOfflineValidationRequiresEveryCPUAndCUDABundle(t *testing.T) {
	err := validateOfflineModels("cpu-mert", "", "cpu-clap", "cuda-clap")
	if err == nil || !strings.Contains(err.Error(), "CUDA MERT") {
		t.Fatalf("missing CUDA MERT bundle error = %v", err)
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
