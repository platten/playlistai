package localaudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func testArtifact(name, role string, data []byte, executable bool) Artifact {
	hash := sha256.Sum256(data)
	return Artifact{Name: name, Role: role, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), Executable: executable}
}

func testManifest(files map[string][]byte) Manifest {
	return Manifest{
		SchemaVersion: 1, ID: "ffmpeg-8.1.2-linux-amd64-test-v1", Platform: runtime.GOOS + "/" + runtime.GOARCH,
		FFmpegVersion: "8.1.2", SourceURL: "https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz",
		SourceSHA256: "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c",
		License:      "LGPL-2.1-or-later", NetworkDisabled: true,
		EnabledProtocols: []string{"file", "pipe"}, EnabledDemuxers: []string{"flac", "mp3", "aac", "mov", "wav"},
		EnabledDecoders: []string{"flac", "mp3float", "aac", "pcm_f32le"},
		Artifacts: []Artifact{
			testArtifact("ffmpeg", "ffmpeg", files["ffmpeg"], true),
			testArtifact("ffprobe", "ffprobe", files["ffprobe"], true),
			testArtifact("LICENSES.txt", "licenses", files["LICENSES.txt"], false),
			testArtifact("build-info.txt", "build_info", files["build-info.txt"], false),
		},
	}
}

func testPayload(t *testing.T) fs.FS {
	t.Helper()
	files := map[string][]byte{
		"ffmpeg": []byte("executable-one"), "ffprobe": []byte("executable-two"),
		"LICENSES.txt": []byte("license"), "build-info.txt": []byte("build"),
	}
	manifest := testManifest(files)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	payload := fstest.MapFS{ManifestName: &fstest.MapFile{Data: raw, Mode: 0o600}}
	for name, data := range files {
		payload[name] = &fstest.MapFile{Data: data, Mode: 0o600}
	}
	return payload
}

func TestInstallPayloadValidatesAndReusesGeneration(t *testing.T) {
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := InstallPayload(context.Background(), testPayload(t), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := InstallPayload(context.Background(), testPayload(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Directory() != second.Directory() || first.ID() != second.ID() {
		t.Fatalf("runtime generation was not reused: %q %q", first.Directory(), second.Directory())
	}
	manifestCopy := first.Manifest()
	manifestCopy.Artifacts[0].Name = "changed"
	if first.Manifest().Artifacts[0].Name == "changed" {
		t.Fatal("runtime manifest exposed mutable internal storage")
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		info, err := os.Stat(filepath.Join(first.Directory(), name))
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("executable %s mode: %v %v", name, info, err)
		}
	}
}

func TestInstallPayloadRejectsCorruptionAndUnsafeRuntime(t *testing.T) {
	root, _ := filepath.Abs(t.TempDir())
	payload := testPayload(t).(fstest.MapFS)
	payload["ffmpeg"] = &fstest.MapFile{Data: []byte("changed")}
	if _, err := InstallPayload(context.Background(), payload, root); err == nil {
		t.Fatal("corrupt artifact accepted")
	}
	if _, err := OpenRuntime("relative"); err == nil {
		t.Fatal("relative runtime accepted")
	}
}

func TestManifestRejectsNetworkAndMissingCodec(t *testing.T) {
	files := map[string][]byte{"ffmpeg": []byte("a"), "ffprobe": []byte("b"), "LICENSES.txt": []byte("c"), "build-info.txt": []byte("d")}
	manifest := testManifest(files)
	manifest.EnabledProtocols = append(manifest.EnabledProtocols, "https")
	if err := manifest.Validate(); err == nil {
		t.Fatal("network protocol accepted")
	}
	manifest = testManifest(files)
	manifest.EnabledDecoders = []string{"flac", "aac"}
	if err := manifest.Validate(); err == nil {
		t.Fatal("missing MP3 decoder accepted")
	}
}
