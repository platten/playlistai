package discoveryasset

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/modelpack"
	"github.com/platten/playlistai/internal/ports"
)

const ArchiveProgressOp = "discovery-archive"

type Update struct {
	Version         string `json:"version"`
	Digest          string `json:"digest"`
	DownloadBytes   int64  `json:"downloadBytes"`
	Files           int    `json:"files"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Source          string `json:"source"`
	Format          string `json:"format"`
}
type ArchiveResult struct {
	Directory      string `json:"directory"`
	Files          int    `json:"files"`
	DownloadBytes  int64  `json:"downloadBytes"`
	ManifestDigest string `json:"manifestDigest"`
}
type remoteManifest struct {
	location  string
	raw       []byte
	digest    string
	multipart *modelpack.Manifest
	discovery *Manifest
	update    Update
}

func fetchRemote(ctx context.Context, location string) (remoteManifest, error) {
	r := remoteManifest{location: location}
	if !validURL(location) {
		return r, errors.New("discoveryasset: manifest requires HTTPS")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if e != nil {
		return r, e
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, e := downloadClient().Do(req)
	if e != nil {
		return r, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("discoveryasset: manifest HTTP %d", resp.StatusCode)
	}
	r.raw, e = io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil {
		return r, e
	}
	if len(r.raw) > 4<<20 {
		return r, errors.New("discoveryasset: manifest exceeds 4 MiB")
	}
	if resp.Request != nil {
		r.location = resp.Request.URL.String()
	}
	var header struct {
		Format string `json:"format"`
	}
	if e = json.Unmarshal(r.raw, &header); e != nil {
		return r, e
	}
	if header.Format == Format {
		var m Manifest
		if e = json.Unmarshal(r.raw, &m); e != nil {
			return r, e
		}
		if m.hasActivationFields() {
			return r, errors.New("discoveryasset: remote manifest contains local activation fields")
		}
		if e = m.Validate(); e != nil {
			return r, e
		}
		r.discovery = &m
		r.digest = manifestHash(m)
		r.update = Update{Version: m.Version, Digest: r.digest, DownloadBytes: m.TotalBytes(), Files: len(m.Packs) + 1, Source: "hosted", Format: Format}
		return r, nil
	}
	if header.Format == "playlist-ai-paipack-parts" {
		return r, errors.New("discoveryasset: this manifest contains manual paipack transport parts; create a desktop-compatible hosted bundle with paipack-split --hosted and publish its parts and manifest")
	}
	var m modelpack.Manifest
	if e = json.Unmarshal(r.raw, &m); e != nil {
		return r, e
	}
	if e = validateMultipart(m, int64(len(r.raw))); e != nil {
		return r, e
	}
	r.multipart = &m
	r.digest = manifestHash(m)
	total := int64(len(r.raw))
	for _, part := range m.Parts {
		total += part.Size
	}
	r.update = Update{Version: "multipart-" + r.digest[:16], Digest: r.digest, DownloadBytes: total, Files: len(m.Parts), Source: "hosted", Format: "modelpack-v1"}
	return r, nil
}
func manifestHash(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func validateMultipart(m modelpack.Manifest, manifestBytes int64) error {
	if e := m.Validate(); e != nil {
		return e
	}
	if len(m.Files) > 128 {
		return errors.New("discoveryasset: too many hosted packs")
	}
	total := manifestBytes
	for _, part := range m.Parts {
		if part.Size > MaxDownloadBytes-total {
			return errors.New("discoveryasset: multipart download exceeds 3 GB")
		}
		total += part.Size
	}
	total = 0
	for _, f := range m.Files {
		if !strings.HasSuffix(strings.ToLower(f.Path), ".paipack") || f.Size <= 0 || f.Size > MaxDownloadBytes-total {
			return errors.New("discoveryasset: multipart files must be v5 paipacks totaling at most 3 GB")
		}
		total += f.Size
	}
	return nil
}
func (m *Manager) CheckUpdate(ctx context.Context, location string) (Update, error) {
	r, e := fetchRemote(ctx, location)
	if e != nil {
		return Update{}, e
	}
	status := m.Status()
	r.update.UpdateAvailable = !status.Installed || status.Source != "hosted" || status.ManifestDigest != r.digest
	return r.update, nil
}

type progressLabel struct {
	p  ports.Progress
	op string
}

func (p progressLabel) Report(_ string, done, total int64, note string) {
	if p.p != nil {
		p.p.Report(p.op, done, total, note)
	}
}

func (m *Manager) installMultipart(ctx context.Context, r remoteManifest, p ports.Progress) (Status, error) {
	var expanded int64
	for _, f := range r.multipart.Files {
		expanded += f.Size
	}
	// Nested paipack expansion is separately checked while staging. Reserve
	// transport/extraction space before downloading any bytes.
	if e := checkDisk(m.root, r.update.DownloadBytes+expanded*3+(256<<20)); e != nil {
		return m.Status(), e
	}
	work, e := os.MkdirTemp(m.root, ".multipart-")
	if e != nil {
		return m.Status(), e
	}
	defer os.RemoveAll(work)
	destination := filepath.Join(work, "unpacked")
	if e = modelpack.FetchManifest(ctx, *r.multipart, r.location, filepath.Join(m.root, "transport-cache"), destination, progressLabel{p, ProgressOp}); e != nil {
		return m.Status(), e
	}
	paths := make([]string, 0, len(r.multipart.Files))
	for _, f := range r.multipart.Files {
		paths = append(paths, filepath.Join(destination, filepath.FromSlash(f.Path)))
	}
	return m.installArchives(ctx, paths, r.update.Version, "hosted", r.digest, r.update.DownloadBytes, r.location, p)
}

// ImportLocal preserves the selected pack's bytes and publishes a persistent
// shared-data override. It does not import a personal music library or refit it.
func (m *Manager) ImportLocal(ctx context.Context, path string, p ports.Progress) (Status, error) {
	if !m.installing.CompareAndSwap(false, true) {
		return m.Status(), librarypack.ErrMutationInProgress
	}
	defer m.installing.Store(false)
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return m.Status(), librarypack.ErrManagerClosed
	}
	info, e := os.Stat(path)
	if e != nil {
		return m.Status(), e
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxDownloadBytes {
		return m.Status(), errors.New("discoveryasset: choose a regular paipack no larger than 3 GB")
	}
	file, e := describeFile(ctx, path, "https://local.invalid")
	if e != nil {
		return m.Status(), e
	}
	return m.installArchives(ctx, []string{path}, "local-"+file.SHA256[:16], "local", file.SHA256, 0, "https://local.invalid", p)
}

func (m *Manager) installArchives(ctx context.Context, paths []string, version, source, digest string, transportBytes int64, location string, p ports.Progress) (Status, error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	if e := ctx.Err(); e != nil {
		return m.Status(), e
	}
	dir, e := os.MkdirTemp(m.root, "release-")
	if e != nil {
		return m.Status(), e
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	manifest := Manifest{Format: Format, SchemaVersion: 1, Version: version, Source: source, ManifestDigest: digest, TransportFormat: "modelpack-v1", TransportBytes: transportBytes}
	if source == "local" {
		manifest.TransportFormat = "paipack-v5"
	}
	managers := make([]*librarypack.Manager, 0, len(paths))
	defer func() {
		for _, pm := range managers {
			_ = pm.Close()
		}
	}()
	seen := map[string]bool{}
	var expanded int64
	for i, path := range paths {
		p.Report(ProgressOp, int64(i), int64(len(paths)), "Verifying music discovery pack")
		declared, inspectErr := inspectPackManifest(ctx, path)
		if inspectErr != nil {
			return m.Status(), inspectErr
		}
		var declaredBytes int64
		for _, member := range declared.Files {
			declaredBytes += member.Size
		}
		if declaredBytes > 12_000_000_000-expanded {
			return m.Status(), errors.New("discoveryasset: expanded set exceeds 12 GB")
		}
		if err := checkDisk(m.root, declaredBytes*3+(256<<20)); err != nil {
			return m.Status(), err
		}
		stageDir := filepath.Join(dir, fmt.Sprintf("staged-%03d", i))
		limits := packLimits()
		limits.MaxExpandedBytes = declaredBytes + (4 << 20)
		pm, err := librarypack.OpenManager(ctx, stageDir, limits)
		if err != nil {
			return m.Status(), err
		}
		staged, err := pm.Stage(ctx, path)
		if err != nil {
			_ = pm.Close()
			return m.Status(), err
		}
		pack := staged.Manifest()
		if pack.PackID != declared.PackID {
			_ = pm.Discard(staged)
			_ = pm.Close()
			return m.Status(), errors.New("discoveryasset: pack changed after expansion preflight")
		}
		var size int64
		for _, f := range pack.Files {
			size += f.Size
		}
		expanded += size
		if expanded > 12_000_000_000 || seen[pack.PackID] {
			_ = pm.Discard(staged)
			_ = pm.Close()
			return m.Status(), errors.New("discoveryasset: duplicate pack or expanded set exceeds 12 GB")
		}
		seen[pack.PackID] = true
		if err = checkDisk(m.root, size*2+(256<<20)); err == nil {
			p.Report(ProgressOp, int64(i), int64(len(paths)), "Building local discovery search indexes (this can take several minutes)")
			err = localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1, MaxScratchBytes: 256 << 20})
		}
		if err != nil {
			_ = pm.Discard(staged)
			_ = pm.Close()
			return m.Status(), err
		}
		packHash := staged.PackSHA256()
		if source == "local" && packHash != digest {
			_ = pm.Discard(staged)
			_ = pm.Close()
			return m.Status(), errors.New("discoveryasset: local pack changed while importing")
		}
		if err = pm.Activate(ctx, staged); err != nil {
			_ = pm.Close()
			return m.Status(), err
		}
		_ = pm.Close()
		target := filepath.Join(dir, pack.PackID)
		if err = os.Rename(stageDir, target); err != nil {
			return m.Status(), err
		}
		pm, err = librarypack.OpenManager(ctx, target, packLimits())
		if err != nil {
			return m.Status(), err
		}
		managers = append(managers, pm)
		info, err := os.Stat(path)
		if err != nil {
			return m.Status(), err
		}
		manifest.Packs = append(manifest.Packs, File{Name: fmt.Sprintf("pack-%03d.paipack", i+1), URL: location, Size: info.Size(), SHA256: packHash, PackID: pack.PackID, ExpandedBytes: size})
	}
	p.Report(ProgressOp, 0, 1, "Indexing artist, album and mood profiles")
	if e = createCompanionSources(ctx, filepath.Join(dir, "discovery.sqlite"), manifest, func(i int, yield func(librarypack.Track) error) error {
		lease, err := managers[i].Pin()
		if err != nil {
			return err
		}
		defer lease.Release()
		stream, err := lease.Generation().OpenTrackSource(ctx)
		if err != nil {
			return err
		}
		defer stream.Close()
		for {
			track, ok, err := stream.Next(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if err = yield(track); err != nil {
				return err
			}
		}
	}); e != nil {
		return m.Status(), e
	}
	manifest.Companion, e = describeFile(ctx, filepath.Join(dir, "discovery.sqlite"), location)
	if e != nil {
		return m.Status(), e
	}
	if e = manifest.Validate(); e != nil {
		return m.Status(), e
	}
	status, committed, e := m.activate(ctx, dir, manifest, p)
	keep = committed
	return status, e
}

// DownloadArchive saves the freshly fetched manifest and its referenced
// transport files to a new directory; it never installs or changes active data.
func DownloadArchive(ctx context.Context, location, destination string, p ports.Progress) (ArchiveResult, error) {
	r, e := fetchRemote(ctx, location)
	if e != nil {
		return ArchiveResult{}, e
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	abs, e := filepath.Abs(destination)
	if e != nil {
		return ArchiveResult{}, e
	}
	if _, e = os.Lstat(abs); !os.IsNotExist(e) {
		return ArchiveResult{}, errors.New("discoveryasset: archive destination must be a new directory")
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return ArchiveResult{}, e
	}
	if e = checkDisk(filepath.Dir(abs), 2*r.update.DownloadBytes+(4<<20)); e != nil {
		return ArchiveResult{}, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(abs), ".discovery-archive-")
	if e != nil {
		return ArchiveResult{}, e
	}
	defer os.RemoveAll(stage)
	cache, e := os.MkdirTemp(filepath.Dir(abs), ".discovery-archive-cache-")
	if e != nil {
		return ArchiveResult{}, e
	}
	defer os.RemoveAll(cache)
	type download struct {
		name, url string
		size      int64
		hash      string
	}
	var files []download
	if r.multipart != nil {
		for _, part := range r.multipart.Parts {
			name := part.Path
			source := part.Path
			if validURL(source) {
				u, _ := url.Parse(source)
				name = filepath.Base(u.Path)
				if !safeName.MatchString(name) {
					return ArchiveResult{}, errors.New("discoveryasset: absolute part URL needs a portable file name")
				}
				portable := *r.multipart
				portable.Parts = []modelpack.Part{{Path: name, Size: part.Size, SHA256: part.SHA256}}
				if err := portable.Validate(); err != nil {
					return ArchiveResult{}, errors.New("discoveryasset: absolute part URL has a non-portable file name")
				}
			} else {
				base, _ := url.Parse(r.location)
				source = base.ResolveReference(&url.URL{Path: part.Path}).String()
			}
			files = append(files, download{name, source, part.Size, strings.ToLower(part.SHA256)})
		}
	} else {
		for _, f := range append(append([]File(nil), r.discovery.Packs...), r.discovery.Companion) {
			files = append(files, download{f.Name, f.URL, f.Size, f.SHA256})
		}
	}
	seen := map[string]bool{"manifest.json": true, "manifest.offline.json": true}
	for _, f := range files {
		key := strings.ToLower(filepath.ToSlash(f.name))
		if seen[key] || strings.HasPrefix(key, "manifest.json/") {
			return ArchiveResult{}, errors.New("discoveryasset: duplicate or reserved archive file name")
		}
		seen[key] = true
	}
	for key := range seen {
		for parent := filepath.ToSlash(filepath.Dir(key)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return ArchiveResult{}, errors.New("discoveryasset: conflicting archive file paths")
			}
		}
	}
	var done int64
	for _, f := range files {
		target := filepath.Join(stage, filepath.FromSlash(f.name))
		cached := filepath.Join(cache, f.hash)
		base := done
		_, e = dataset.DownloadWithClient(ctx, f.url, cached, f.size, f.hash, func(n, _ int64) {
			p.Report(ArchiveProgressOp, base+n, r.update.DownloadBytes, "Saving discovery archive")
		}, downloadClient())
		if e != nil {
			return ArchiveResult{}, e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return ArchiveResult{}, e
		}
		if e = copyFile(ctx, cached, target); e != nil {
			return ArchiveResult{}, e
		}
		done += f.size
	}
	if e = os.WriteFile(filepath.Join(stage, "manifest.json"), r.raw, 0600); e != nil {
		return ArchiveResult{}, e
	}
	extraFiles := 0
	if r.multipart != nil {
		offline := *r.multipart
		offline.Parts = append([]modelpack.Part(nil), r.multipart.Parts...)
		changed := false
		for i := range offline.Parts {
			if validURL(offline.Parts[i].Path) {
				offline.Parts[i].Path = files[i].name
				changed = true
			}
		}
		if changed {
			raw, err := json.MarshalIndent(offline, "", "  ")
			if err != nil {
				return ArchiveResult{}, err
			}
			if err = os.WriteFile(filepath.Join(stage, "manifest.offline.json"), raw, 0600); err != nil {
				return ArchiveResult{}, err
			}
			extraFiles = 1
		}
	}
	if e = ctx.Err(); e != nil {
		return ArchiveResult{}, e
	}
	if _, e = os.Lstat(abs); !os.IsNotExist(e) {
		return ArchiveResult{}, errors.New("discoveryasset: archive destination appeared during download")
	}
	if e = os.Rename(stage, abs); e != nil {
		return ArchiveResult{}, e
	}
	done += int64(len(r.raw))
	p.Report(ArchiveProgressOp, done, done, "Archive saved")
	return ArchiveResult{Directory: abs, Files: len(files) + 1 + extraFiles, DownloadBytes: done, ManifestDigest: r.digest}, nil
}

func inspectPackManifest(ctx context.Context, path string) (librarypack.Manifest, error) {
	var manifest librarypack.Manifest
	file, e := os.Open(path)
	if e != nil {
		return manifest, e
	}
	defer file.Close()
	zr, e := zstd.NewReader(&contextReader{ctx: ctx, r: file}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(128<<20))
	if e != nil {
		return manifest, e
	}
	defer zr.Close()
	reader := tar.NewReader(zr)
	header, e := reader.Next()
	if e != nil {
		return manifest, e
	}
	if header.Name != librarypack.ManifestName || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 4<<20 {
		return manifest, errors.New("discoveryasset: pack must begin with its bounded v5 manifest")
	}
	raw, e := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if e != nil {
		return manifest, e
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return manifest, e
	}
	return manifest, manifest.Validate(packLimits())
}
