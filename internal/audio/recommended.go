package audio

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

const originalCLAPRevision = "lukewys/laion_clap@4226474:music_audioset_epoch_15_esc_90.14.pt"
const hostedCLAPRoot = "https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/"

// NativeInferenceAvailable reports compiled worker support, not installed models
// or successful runtime health. Pure-Go builds can still use legacy workers.
func NativeInferenceAvailable() bool { return nativeInferenceAvailable }

// RecommendedBundle describes the reviewed original LAION music checkpoint
// contained by the pinned hosted model pack. Its artifact URLs are not used by
// the recommended installer; modelpack downloads and verifies the parts first.
func RecommendedBundle() (BundleManifest, error) {
	if !nativeInferenceAvailable {
		return BundleManifest{}, fmt.Errorf("this application build has no native CLAP worker; use a cgo-enabled desktop build or a legacy custom bundle with its own worker")
	}
	return recommendedBundle(runtime.GOOS + "/" + runtime.GOARCH)
}

func recommendedBundle(platform string) (BundleManifest, error) {
	runtimeDownload, ok := recommendedRuntimes[platform]
	if !ok {
		return BundleManifest{}, fmt.Errorf("no hosted CLAP model pack is published for %s; choose a compatible custom bundle", platform)
	}
	packURL := hostedCLAPRoot + "clap-" + strings.ReplaceAll(platform, "/", "-") + "/manifest.json"
	m := BundleManifest{
		Version: 2, ID: "custom-clap-cpu-v2", Label: "LAION original HTSAT-base music checkpoint · CPU",
		Platform: platform, MemoryBytes: 2 << 30, License: "CC0-1.0 checkpoint; MIT runtime; GPL-3.0 application worker",
		SourceURL:       "https://huggingface.co/lukewys/laion_clap/blob/4226474e38defca6fc9272a7848bb7b0355ccd7a/music_audioset_epoch_15_esc_90.14.pt",
		ONNXOutputNames: []string{"embedding", "embedding"}, TextUnpadded: false,
		Parity: ParityReport{ReferenceRevision: originalCLAPRevision, Fixtures: 10, MaximumAbsoluteError: 0.0000024596229195594788, MinimumCosine: 0.9999999403953552, TokenizerCases: 7, TokenizerExact: true, PreprocessingCases: 3, PreprocessingWithinTolerance: true},
		Model:  core.AudioModelIdentity{Model: "LAION original HTSAT-base music checkpoint", Revision: originalCLAPRevision, Preprocessing: PreprocessingVersion, Runtime: "onnxruntime/1.26.0/cpu", Dimension: 512},
		Artifacts: []BundleArtifact{
			{Role: "runtime", Name: filepath.Base(runtimeDownload.ArchiveMember), URL: packURL, Size: runtimeDownload.UnpackedSize, SHA256: runtimeDownload.UnpackedSHA256},
			{Role: "audio_model", Name: "audio.onnx", URL: packURL, Size: 281357899, SHA256: "22fbaee1757ef4ca03010cc0c27cadff34bf91453fe1963965e2a85cc5d9514b"},
			{Role: "text_model", Name: "text.onnx", URL: packURL, Size: 501364988, SHA256: "0c7502bb27f7ae9605483d81eae198693dcd9683c220ba7366aea8a58f20df16"},
			{Role: "vocabulary", Name: "vocab.json", URL: packURL, Size: 1049622, SHA256: "078c509073469aae3ed14de5dee8f1a36cf4555abe6e88d840e6266b599492eb"},
			{Role: "merges", Name: "merges.txt", URL: packURL, Size: 506319, SHA256: "84809de545b2f3e79275acfaefa4af5055438ddb13d9eed9cff02eacf5cc19fc"},
			{Role: "preprocessing", Name: "preprocessing.json", URL: packURL, Size: 314, SHA256: "df7723e9a726fefb06b95fed9e13f390a19b2a9bc92b65d19fddd5eab4a8711e"},
			{Role: "license", Name: "licenses.txt", URL: packURL, Size: 48668, SHA256: "26ec02e08d6d7f5f39612ecd389b84c1afd2f5ad91a8195d4a879b553f1cf343"},
			{Role: "health", Name: "health.json", URL: packURL, Size: 22893, SHA256: "d79cc3202677519be86e9be19a13f9a26b74534046b06dd5713b11dcf819ba44"},
		},
	}
	m.Model.Weights = m.EmbeddingFingerprint()
	if m.Model.Weights != "f208f3bff6cfd4dee3fc5a274168e7db463842246e1c7a88755a8a80bbb6bc9d" {
		return BundleManifest{}, fmt.Errorf("recommended CLAP embedding identity is inconsistent")
	}
	return m, m.validateRuntimeForPlatform(platform)
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
