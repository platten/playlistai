package audio

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/platten/playlistai/internal/core"
)

// These contain synthetic reference outputs and license/configuration text,
// never model weights or music recordings.
//
//go:embed resources/*
var recommendedResources embed.FS

const publicCLAPRevision = "e9fd5ac1dbf3280936a7fc3ec8a020453ff184db"
const publicCLAPSource = "https://huggingface.co/Xenova/larger_clap_music_and_speech/resolve/" + publicCLAPRevision + "/"

// NativeInferenceAvailable reports compiled worker support, not installed models
// or successful runtime health. Pure-Go builds can still use legacy workers.
func NativeInferenceAvailable() bool { return nativeInferenceAvailable }

// RecommendedBundle chooses the largest precision variant of the supported
// public music CLAP family. Other checkpoints require their own paired export.
func RecommendedBundle() (BundleManifest, error) {
	if !nativeInferenceAvailable {
		return BundleManifest{}, fmt.Errorf("this application build has no native CLAP worker; use a cgo-enabled desktop build or a legacy custom bundle with its own worker")
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	ort, ok := recommendedRuntimes[platform]
	if !ok {
		return BundleManifest{}, fmt.Errorf("no recommended CLAP runtime is published for %s; choose a compatible custom bundle", platform)
	}
	m := BundleManifest{
		Version: 2, ID: "clap-music-speech-fp32-v1", Label: "CLAP Music + Speech · full precision",
		Platform: platform, MemoryBytes: 2 << 30, License: "Apache-2.0 model; MIT ONNX Runtime; GPL-3.0 application worker",
		SourceURL: "https://huggingface.co/Xenova/larger_clap_music_and_speech", ONNXOutputNames: []string{"audio_embeds", "text_embeds"}, TextUnpadded: true,
		Model: core.AudioModelIdentity{Model: "laion/larger_clap_music_and_speech", Revision: "195c3a3e68faebb3e2088b9a79e79b43ddbda76b", Preprocessing: PreprocessingVersion, Runtime: "onnxruntime/1.26.0/cpu", Dimension: 512},
		Artifacts: []BundleArtifact{
			{Role: "audio_model", Name: "audio.onnx", URL: publicCLAPSource + "onnx/audio_model.onnx", Size: 281749092, SHA256: "3ecc72d27740e2a09ced20cf22fd6244122e5e506008763a0f368b3b4ff6eac8"},
			{Role: "text_model", Name: "text.onnx", URL: publicCLAPSource + "onnx/text_model.onnx", Size: 501513769, SHA256: "96c0f248bfaabe5d467958245beb0243e387ed628251e3b407b856848379e89c"},
			{Role: "vocabulary", Name: "vocab.json", URL: publicCLAPSource + "vocab.json", Size: 798293, SHA256: "ed19656ea1707df69134c4af35c8ceda2cc9860bf2c3495026153a133670ab5e"},
			{Role: "merges", Name: "merges.txt", URL: publicCLAPSource + "merges.txt", Size: 456318, SHA256: "1ce1664773c50f3e0cc8842619a93edc4624525b728b188a9e0be33b7726adc5"},
			ort,
		},
	}
	for _, item := range []struct{ role, name string }{{"health", "health.json"}, {"preprocessing", "preprocessing.json"}, {"license", "licenses.txt"}} {
		data, err := recommendedResources.ReadFile("resources/" + item.name)
		if err != nil {
			return m, err
		}
		hash := sha256.Sum256(data)
		m.Artifacts = append(m.Artifacts, BundleArtifact{Role: item.role, Name: item.name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), Data: data})
	}
	parity, err := recommendedResources.ReadFile("resources/parity.json")
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(parity, &m.Parity); err != nil {
		return m, err
	}
	m.Model.Weights = m.EmbeddingFingerprint()
	return m, m.Validate()
}

func runtimeArtifact(target, extension, archiveHash, library, libraryHash string, archiveSize, librarySize int64) BundleArtifact {
	base := "onnxruntime-" + target + "-1.26.0"
	member := base + "/lib/" + library
	if target == "osx-arm64" {
		member = "./" + member
	}
	return BundleArtifact{Role: "runtime", Name: base + extension, URL: "https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/" + base + extension, Size: archiveSize, SHA256: archiveHash, ArchiveMember: member, UnpackedSize: librarySize, UnpackedSHA256: libraryHash}
}

var recommendedRuntimes = map[string]BundleArtifact{
	"linux/amd64":   runtimeArtifact("linux-x64", ".tgz", "1254da24fb389cf39dc0ff3451ab48301740ffbfcbaf646849df92f80ee92c57", "libonnxruntime.so.1.26.0", "5bd5bedf736fc501692435d0ec4f6e8b2bdf48cd30af8e6d00d61b3ddc9a7ab8", 8590023, 23023576),
	"linux/arm64":   runtimeArtifact("linux-aarch64", ".tgz", "34ff1c2d0f12e2cf3d33a0c5f82e39792e1d581fbd6968fd7c30d173654be01a", "libonnxruntime.so.1.26.0", "115ecb838e703d390262b8b4d07d5248e6693c67658d4c98c48f94905ab27af4", 7608947, 19543040),
	"darwin/arm64":  runtimeArtifact("osx-arm64", ".tgz", "7a1280bbb1701ea514f71828765237e7896e0f2e1cd332f1f70dbd5c3e33aca3", "libonnxruntime.1.26.0.dylib", "30afadcfc3c704f7671f8430d6252956651c1972373901d2be629da2e6a4d8ee", 31717869, 37310032),
	"windows/amd64": runtimeArtifact("win-x64", ".zip", "6ebe99b5564bf4d029b6e93eac9ff423682b6212eade769e9ca3f685eaf500b4", "onnxruntime.dll", "b2ba7ca16e0e4fe71ad5148744ab885a2f5809e52a0c3de4d9ba3853a03977f9", 75675381, 14897976),
	"windows/arm64": runtimeArtifact("win-arm64", ".zip", "852e89621fb752b261821dda131042ed7c7fa18d8bb06768bd4eb7fa7086d87f", "onnxruntime.dll", "01bba4af5089b1951df8b6e6e960e77313103e872519d57304c0ddc3b002a33b", 77548904, 15041848),
}
