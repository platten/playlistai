package localaudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/installlock"
)

type Runtime struct {
	directory string
	ffmpeg    string
	ffprobe   string
	manifest  Manifest
	limits    Limits
}

func (r *Runtime) ID() string { return r.manifest.ID }
func (r *Runtime) Manifest() Manifest {
	manifest := r.manifest
	manifest.EnabledProtocols = append([]string(nil), r.manifest.EnabledProtocols...)
	manifest.EnabledDemuxers = append([]string(nil), r.manifest.EnabledDemuxers...)
	manifest.EnabledDecoders = append([]string(nil), r.manifest.EnabledDecoders...)
	manifest.EnabledMuxers = append([]string(nil), r.manifest.EnabledMuxers...)
	manifest.Configure = append([]string(nil), r.manifest.Configure...)
	manifest.Artifacts = append([]Artifact(nil), r.manifest.Artifacts...)
	return manifest
}
func (r *Runtime) Directory() string { return r.directory }
func (r *Runtime) WithLimits(limits Limits) *Runtime {
	copy := *r
	copy.limits = limits
	return &copy
}

func artifactByRole(m Manifest, role string) (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.Role == role {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func validateRegular(path string, artifact Artifact) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != artifact.Size || artifact.Executable && !executableModeValid(info) {
		return fmt.Errorf("localaudio: invalid codec artifact %s", artifact.Name)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return fmt.Errorf("localaudio: codec artifact checksum mismatch: %s", artifact.Name)
	}
	return nil
}

// OpenRuntime validates an absolute, versioned runtime directory. It never
// searches PATH or resolves a user-selected executable name.
func OpenRuntime(directory string) (*Runtime, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("localaudio: runtime directory must be an absolute clean path")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("localaudio: runtime directory must not be a symlink")
	}
	manifest, err := readManifest(directory)
	if err != nil {
		return nil, err
	}
	for _, artifact := range manifest.Artifacts {
		if err := validateRegular(filepath.Join(directory, artifact.Name), artifact); err != nil {
			return nil, err
		}
	}
	ffmpeg, _ := artifactByRole(manifest, "ffmpeg")
	ffprobe, _ := artifactByRole(manifest, "ffprobe")
	return &Runtime{
		directory: directory,
		ffmpeg:    filepath.Join(directory, ffmpeg.Name),
		ffprobe:   filepath.Join(directory, ffprobe.Name),
		manifest:  manifest,
		limits:    DefaultLimits(),
	}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// InstallPayload extracts a package-owned payload filesystem into a private,
// versioned directory. The caller authenticates the outer bundle or embeds it;
// this function validates every inner artifact before atomic promotion.
func InstallPayload(ctx context.Context, payload fs.FS, runtimeRoot string) (*Runtime, error) {
	if payload == nil || !filepath.IsAbs(runtimeRoot) || filepath.Clean(runtimeRoot) != runtimeRoot {
		return nil, fmt.Errorf("localaudio: payload and absolute clean runtime root are required")
	}
	manifestFile, err := payload.Open(ManifestName)
	if err != nil {
		return nil, err
	}
	manifest, manifestErr := decodeManifest(contextReader{ctx: ctx, r: manifestFile})
	closeErr := manifestFile.Close()
	if err := errors.Join(manifestErr, closeErr); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		return nil, err
	}
	release, err := installlock.TryAcquire(filepath.Join(runtimeRoot, ".install.lock"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	destination := filepath.Join(runtimeRoot, manifest.ID)
	if existing, err := OpenRuntime(destination); err == nil {
		return existing, nil
	} else if _, statErr := os.Lstat(destination); statErr == nil {
		return nil, fmt.Errorf("localaudio: existing runtime generation is invalid: %w", err)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	stage, err := os.MkdirTemp(runtimeRoot, ".codec-stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	for _, artifact := range manifest.Artifacts {
		if err := copyPayloadArtifact(ctx, payload, stage, artifact); err != nil {
			return nil, err
		}
	}
	raw, err := jsonMarshalManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := writeSynced(filepath.Join(stage, ManifestName), raw, 0o600); err != nil {
		return nil, err
	}
	if _, err := OpenRuntime(stage); err != nil {
		return nil, err
	}
	if err := syncDirectory(stage); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, destination); err != nil {
		return nil, err
	}
	if err := syncDirectory(runtimeRoot); err != nil {
		return nil, err
	}
	return OpenRuntime(destination)
}

func jsonMarshalManifest(manifest Manifest) ([]byte, error) {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func copyPayloadArtifact(ctx context.Context, payload fs.FS, stage string, artifact Artifact) error {
	source, err := payload.Open(artifact.Name)
	if err != nil {
		return err
	}
	defer source.Close()
	mode := os.FileMode(0o600)
	if artifact.Executable {
		mode = 0o700
	}
	target := filepath.Join(stage, artifact.Name)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx: ctx, r: source}, artifact.Size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != artifact.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return fmt.Errorf("localaudio: payload artifact integrity mismatch: %s", artifact.Name)
	}
	return nil
}

func writeSynced(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}
