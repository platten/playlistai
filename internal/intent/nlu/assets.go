package nlu

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/nluresources"
	"github.com/platten/playlistai/internal/ports"
)

const AssetsVersion = "intent-encoders-v1"
const ProgressOp = "intent-models"

// CheckPackagedRuntime verifies compiled support and embedded Windows dependency
// closure without loading a model, touching user data, or accessing the network.
func CheckPackagedRuntime() error {
	if err := PlatformSupportError(); err != nil {
		return err
	}
	if !audio.NativeInferenceAvailable() {
		return fmt.Errorf("native intent inference requires a cgo-enabled application build")
	}
	if _, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH); err != nil {
		return err
	}
	for _, name := range audio.MERTWindowsRuntimeDependencies(runtime.GOOS + "/" + runtime.GOARCH) {
		if _, err := nluresources.Files.ReadFile("resources/" + runtime.GOARCH + "-" + name); err != nil {
			return fmt.Errorf("packaged native intent dependency missing: %s", name)
		}
	}
	return nil
}

//go:embed sources.json
var sourceJSON []byte

//go:embed licenses.txt
var licenseText []byte

type Source struct {
	Model   string `json:"model"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	License string `json:"license"`
	Setup   bool   `json:"setup"`
}

func Sources() []Source {
	var out []Source
	if err := json.Unmarshal(sourceJSON, &out); err != nil {
		panic(err)
	}
	return out
}

func ModelSHA256(kind ModelKind) string {
	for _, s := range Sources() {
		if s.Model == string(kind) && (s.Name == "model.onnx" || s.Name == "onnx/model.onnx") {
			return s.SHA256
		}
	}
	return ""
}

// AssetsIdentity changes when any pinned model or tokenizer input changes.
func AssetsIdentity() string {
	h := sha256.Sum256(sourceJSON)
	return AssetsVersion + "/" + hex.EncodeToString(h[:])
}

func AssetDir(root string) string { return filepath.Join(root, AssetsVersion) }

func sourcePath(dir string, s Source) string {
	name := strings.ReplaceAll(s.Name, "/", "_")
	if s.Name == "onnx/model.onnx" {
		name = "model.onnx"
	}
	return filepath.Join(dir, s.Model, name)
}

func verified(path string, size int64, checksum string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != size {
		return false
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	return err == nil && hex.EncodeToString(h.Sum(nil)) == checksum
}

func RuntimePath(dir string) (string, error) {
	a, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runtime", filepath.Base(a.ArchiveMember)), nil
}

// AssetsReady performs integrity verification, not just a marker-file check.
// Call at activation; parsing keeps an immutable active worker configuration.
func AssetsReady(root string) bool {
	dir := AssetDir(root)
	for _, s := range Sources() {
		if s.Setup && !verified(sourcePath(dir, s), s.Size, s.SHA256) {
			return false
		}
	}
	a, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH)
	if err != nil {
		return false
	}
	p, _ := RuntimePath(dir)
	if !verified(p, a.UnpackedSize, a.UnpackedSHA256) {
		return false
	}
	return stageDependencies(dir, false) == nil
}

func SetupBytes() int64 {
	var n int64
	for _, s := range Sources() {
		if s.Setup {
			n += s.Size
		}
	}
	if a, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH); err == nil {
		n += a.Size
	}
	return n
}

// InstallAssets is used by desktop setup and the offline Go preparation CLI.
// The caller serializes installs. Downloads are resumable and checksum pinned;
// activation and worker health occur separately after every file is verified.
func InstallAssets(ctx context.Context, root string, p ports.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := CheckPackagedRuntime(); err != nil {
		return err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	dir := AssetDir(root)
	if err := os.MkdirAll(filepath.Join(dir, "runtime"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "licenses.txt"), licenseText, 0o600); err != nil {
		return err
	}
	if err := stageDependencies(dir, true); err != nil {
		return err
	}
	var done int64
	total := SetupBytes()
	for _, s := range Sources() {
		if !s.Setup {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		target := sourcePath(dir, s)
		if !verified(target, s.Size, s.SHA256) {
			packed, err := installEmbedded(ctx, target, s)
			if err != nil {
				return err
			}
			if !packed {
				_, err = dataset.Download(ctx, s.URL, target, s.Size, s.SHA256, func(n, _ int64) { p.Report(ProgressOp, done+n, total, "Downloading "+s.Model+" "+s.Name) })
				if err != nil {
					return fmt.Errorf("intent model %s: %w", s.Model, err)
				}
			}
		}
		done += s.Size
		p.Report(ProgressOp, done, total, "Verified "+s.Model+" "+s.Name)
	}
	a, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH)
	if err != nil {
		return err
	}
	runtimeDir := filepath.Join(dir, "runtime")
	lib, _ := RuntimePath(dir)
	if !verified(lib, a.UnpackedSize, a.UnpackedSHA256) {
		packed, err := installEmbedded(ctx, lib, Source{Model: "runtime-" + runtime.GOARCH, Size: a.UnpackedSize, SHA256: a.UnpackedSHA256})
		if err != nil {
			return err
		}
		if !packed {
			archive := filepath.Join(runtimeDir, a.Name)
			if !verified(archive, a.Size, a.SHA256) {
				if _, err = dataset.Download(ctx, a.URL, archive, a.Size, a.SHA256, func(n, _ int64) { p.Report(ProgressOp, done+n, total, "Downloading native text runtime") }); err != nil {
					return err
				}
			}
			if err = audio.UnpackNativeRuntime(ctx, runtimeDir, a); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.Report(ProgressOp, total, total, "Verified intent model assets")
	return nil
}

// Windows dependencies are included in the release executable by build staging.
// No redistributable installer or user-installed Python is required at runtime.
func stageDependencies(dir string, write bool) error {
	for _, name := range audio.MERTWindowsRuntimeDependencies(runtime.GOOS + "/" + runtime.GOARCH) {
		data, err := nluresources.Files.ReadFile("resources/" + runtime.GOARCH + "-" + name)
		if err != nil {
			return fmt.Errorf("this development build lacks the packaged native text runtime dependencies; run scripts/stage-nlu-runtime.py before building")
		}
		h := sha256.Sum256(data)
		target := filepath.Join(dir, "runtime", name)
		if verified(target, int64(len(data)), hex.EncodeToString(h[:])) {
			continue
		}
		if !write {
			return fmt.Errorf("native text runtime dependency unavailable")
		}
		if err := os.WriteFile(target+".tmp", data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(target+".tmp", target); err != nil {
			return err
		}
	}
	if write {
		for _, name := range []string{"Visual-C-V14-License-Redistributable_and_Runtime_ENU.docx", "Visual-Studio-2022-Community-License-EN.docx", "ONNX-LICENSE.txt", "ONNX-ThirdPartyNotices.txt.txt"} {
			if data, err := nluresources.Files.ReadFile("resources/" + name); err == nil {
				if err = os.WriteFile(filepath.Join(dir, "runtime", name), data, 0o600); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func installEmbedded(ctx context.Context, target string, s Source) (bool, error) {
	f, err := nluresources.Files.Open("resources/" + s.Model + "-" + filepath.Base(target))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".embedded-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, copyErr := io.Copy(tmp, io.LimitReader(f, s.Size+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !verified(tmp.Name(), s.Size, s.SHA256) {
		return false, fmt.Errorf("embedded intent model failed integrity check")
	}
	return true, os.Rename(tmp.Name(), target)
}
