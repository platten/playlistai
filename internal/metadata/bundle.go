package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

const BundleVersion = "playlistai-metadata-bundle/v1"
const MetadataProgress = "metadata"
const bundleWindow = 128 << 20

type Artifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type BundleManifest struct {
	Version      string   `json:"version"`
	IndexVersion string   `json:"indexVersion"`
	Catalog      string   `json:"catalog"`
	Date         string   `json:"date"`
	Archive      Artifact `json:"archive"`
	Index        Artifact `json:"index"`
}

func (m BundleManifest) Validate() error {
	if m.Version != BundleVersion || m.IndexVersion != RuntimeVersion || m.Catalog == "" {
		return errors.New("unsupported metadata bundle")
	}
	if _, err := time.Parse("20060102", m.Date); err != nil {
		return err
	}
	for _, a := range []Artifact{m.Archive, m.Index} {
		if a.Name == "" || filepath.Base(a.Name) != a.Name || strings.ContainsAny(a.Name, `/\:?%`) || a.Name == "." || a.Name == ".." || a.Size <= 0 || a.Size > 8<<30 {
			return errors.New("invalid metadata artifact name/size")
		}
		if sum, err := hex.DecodeString(a.SHA256); err != nil || len(sum) != 32 {
			return errors.New("metadata SHA-256 required")
		}
	}
	if !strings.HasSuffix(m.Archive.Name, ".zst") || !strings.HasSuffix(m.Index.Name, ".sqlite") {
		return errors.New("unsupported metadata compression")
	}
	return nil
}

func artifact(ctx context.Context, path string) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, contextReader{ctx, f})
	return Artifact{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

// VerifyBundle exercises the actual wizard decompressor on a local upload
// bundle without activating it or modifying application data.
func VerifyBundle(ctx context.Context, dir string) (BundleManifest, error) {
	var m BundleManifest
	f, err := os.Open(filepath.Join(dir, "metadata-manifest.json"))
	if err != nil {
		return m, err
	}
	err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&m)
	_ = f.Close()
	if err != nil {
		return m, err
	}
	if err = m.Validate(); err != nil {
		return m, err
	}
	a, err := artifact(ctx, filepath.Join(dir, m.Archive.Name))
	if err != nil {
		return m, err
	}
	if a.Size != m.Archive.Size || !strings.EqualFold(a.SHA256, m.Archive.SHA256) {
		return m, errors.New("archive verification failed")
	}
	tmp, err := os.MkdirTemp("", "playlistai-metadata-verify-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	err = expand(ctx, filepath.Join(dir, m.Archive.Name), filepath.Join(tmp, "verified.sqlite"), m, ports.NopProgress{})
	return m, err
}

// Package creates a new upload directory: compact SQLite, zstd, and manifest.
// Only the .zst and JSON need uploading; the uncompressed file aids validation.
func Package(ctx context.Context, source, dir string) (m BundleManifest, err error) {
	if err = os.Mkdir(dir, 0700); err != nil {
		return m, fmt.Errorf("choose a new bundle directory: %w", err)
	}
	index := filepath.Join(dir, "discogs-runtime.sqlite")
	info, err := Compact(ctx, source, index)
	if err != nil {
		return m, err
	}
	m = BundleManifest{Version: BundleVersion, IndexVersion: RuntimeVersion, Catalog: info.Catalog, Date: info.Date}
	if m.Index, err = artifact(ctx, index); err != nil {
		return m, err
	}
	input, err := os.Open(index)
	if err != nil {
		return m, err
	}
	defer input.Close()
	archive := filepath.Join(dir, "discogs-runtime.sqlite.zst")
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return m, err
	}
	defer out.Close()
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(2), zstd.WithWindowSize(bundleWindow))
	if err != nil {
		return m, err
	}
	_, copyErr := io.Copy(zw, contextReader{ctx, input})
	if err = errors.Join(copyErr, zw.Close(), out.Sync(), out.Close()); err != nil {
		return m, err
	}
	if m.Archive, err = artifact(ctx, archive); err != nil {
		return m, err
	}
	if err = m.Validate(); err != nil {
		return m, err
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	err = os.WriteFile(filepath.Join(dir, "metadata-manifest.json"), append(raw, '\n'), 0600)
	return m, err
}

func validateBundleURL(source string) (*url.URL, error) {
	u, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	allowed := u.Scheme == "https" || (u.Scheme == "http" && loopback)
	if u.User != nil || u.Host == "" || !allowed {
		return nil, errors.New("metadata source must use HTTPS (HTTP loopback is allowed for testing)")
	}
	return u, nil
}

func LoadBundle(ctx context.Context, source string) (m BundleManifest, err error) {
	if _, err = validateBundleURL(source); err != nil {
		return m, err
	}
	raw, err := smallGet(ctx, source)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

// Install uses content-addressed files so active readers need not be closed.
// The active pointer changes only after compressed + expanded integrity and
// catalog/format checks succeed. No tar extraction or shell commands are used.
func Install(ctx context.Context, source, dir, catalog string, p ports.Progress) (string, error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	m, err := LoadBundle(ctx, source)
	if err != nil {
		return "", err
	}
	if m.Catalog != catalog {
		return "", errors.New("metadata bundle targets a different recommendation catalog")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "install.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", errors.New("metadata install is already running or has an interrupted lock")
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lock.Name()) }()
	name := "discogs-" + strings.ToLower(m.Index.SHA256) + ".sqlite"
	target := filepath.Join(dir, name)
	if a, err := artifact(ctx, target); err == nil && a.Size == m.Index.Size && strings.EqualFold(a.SHA256, m.Index.SHA256) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return target, activate(dir, name)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("existing content-addressed metadata file failed validation; preserve it elsewhere before retrying")
	}
	u, _ := validateBundleURL(source)
	remote := u.ResolveReference(&url.URL{Path: m.Archive.Name}).String()
	archive := filepath.Join(dir, strings.ToLower(m.Archive.SHA256)+".zst")
	if a, err := artifact(ctx, archive); err != nil || a.Size != m.Archive.Size || !strings.EqualFold(a.SHA256, m.Archive.SHA256) {
		_, err = dataset.Download(ctx, remote, archive, m.Archive.Size, m.Archive.SHA256, func(done, total int64) { p.Report(MetadataProgress, done, total, "Downloading local music metadata") })
		if err != nil {
			return "", err
		}
	}
	if err = expand(ctx, archive, target, m, p); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = activate(dir, name); err != nil {
		return "", err
	}
	_ = os.Remove(archive) // only this verified installer cache, not operator artifacts
	p.Report(MetadataProgress, 1, 1, "Local music metadata ready")
	return target, nil
}

func expand(ctx context.Context, archive, target string, m BundleManifest, p ports.Progress) (err error) {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(contextReader{ctx, f}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(bundleWindow))
	if err != nil {
		return err
	}
	defer zr.Close()
	out, err := os.CreateTemp(filepath.Dir(target), ".metadata-unpack-*.sqlite")
	if err != nil {
		return err
	}
	defer func() { _ = out.Close(); _ = os.Remove(out.Name()) }()
	p.Report(MetadataProgress, 0, m.Index.Size, "Decompressing and checking local music metadata")
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(zr, m.Index.Size+1))
	if err != nil {
		return err
	}
	if n != m.Index.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.Index.SHA256) {
		return errors.New("decompressed metadata size/checksum mismatch")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	s, err := Open(out.Name())
	if err != nil {
		return err
	}
	info := s.Info()
	_ = s.Close()
	if info.Catalog != m.Catalog || info.Version != m.IndexVersion || info.Date != m.Date {
		return errors.New("metadata index does not match its bundle manifest")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return publishIndex(out.Name(), target, false)
}

func activate(dir, name string) (err error) {
	f, err := os.CreateTemp(dir, ".active-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = io.WriteString(f, name); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "active"))
}

func ActivePath(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "active"))
	name := strings.TrimSpace(string(raw))
	if err == nil && filepath.Base(name) == name && strings.HasPrefix(name, "discogs-") && strings.HasSuffix(name, ".sqlite") && !strings.ContainsAny(name, `/\`) {
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, "discogs.sqlite")
}
