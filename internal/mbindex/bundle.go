package mbindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/installlock"
	"github.com/platten/playlistai/internal/ports"
)

const BundleVersion = "playlistai-musicbrainz-bundle/v1"
const DefaultPartBytes int64 = 199_000_000
const ProgressOp = "musicbrainz-metadata"
const GenreVocabularyName = "musicbrainz-genres.json"

const maxGenreVocabularyBytes int64 = 4 << 20

// Keep accepting the larger window used by previously published bundles.
const bundleWindow = 128 << 20
const bundleEncoderWindow = 8 << 20

type Artifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type BundleManifest struct {
	Version         string     `json:"version"`
	IndexVersion    string     `json:"indexVersion"`
	Snapshot        string     `json:"snapshot"`
	CoreLicense     string     `json:"coreLicense"`
	TagsLicense     string     `json:"tagsLicense"`
	Index           Artifact   `json:"index"`
	Parts           []Artifact `json:"parts"`
	GenreVocabulary *Artifact  `json:"genreVocabulary,omitempty"`
}

type BundleProgress struct {
	Stage string
	Done  int64
	Total int64
}

func (m BundleManifest) Validate() error {
	if m.Version != BundleVersion || m.IndexVersion != IndexVersion || m.CoreLicense != "CC0-1.0" || m.TagsLicense != "CC-BY-NC-SA-3.0" {
		return errors.New("unsupported MusicBrainz bundle")
	}
	if _, err := time.Parse("20060102-150405", m.Snapshot); err != nil {
		return err
	}
	if err := validateArtifact(m.Index, 32<<30, ".sqlite"); err != nil {
		return err
	}
	if len(m.Parts) == 0 || len(m.Parts) > 1000 {
		return errors.New("MusicBrainz bundle has no usable parts")
	}
	for i, part := range m.Parts {
		if err := validateArtifact(part, 200_000_000, fmt.Sprintf(".part-%05d", i+1)); err != nil {
			return err
		}
	}
	if m.GenreVocabulary != nil {
		if m.GenreVocabulary.Name != GenreVocabularyName {
			return errors.New("invalid MusicBrainz genre vocabulary artifact")
		}
		if err := validateArtifact(*m.GenreVocabulary, maxGenreVocabularyBytes, ".json"); err != nil {
			return err
		}
	}
	return nil
}

func validateArtifact(a Artifact, max int64, suffix string) error {
	if a.Name == "" || filepath.Base(a.Name) != a.Name || strings.ContainsAny(a.Name, `/\:?%`) || !strings.HasSuffix(a.Name, suffix) || a.Size <= 0 || a.Size > max {
		return errors.New("invalid MusicBrainz artifact")
	}
	sum, err := hex.DecodeString(a.SHA256)
	if err != nil || len(sum) != 32 {
		return errors.New("MusicBrainz artifact SHA-256 required")
	}
	return nil
}

func Package(ctx context.Context, index, dir string, partBytes int64) (m BundleManifest, err error) {
	return PackageWithProgress(ctx, index, dir, partBytes, nil)
}

func PackageWithProgress(ctx context.Context, index, dir string, partBytes int64, progress func(BundleProgress)) (m BundleManifest, err error) {
	return PackageBundle(ctx, BundlePackageOptions{Index: index, Directory: dir, PartBytes: partBytes, Progress: progress})
}

type BundlePackageOptions struct {
	Index           string
	GenreVocabulary string
	Directory       string
	PartBytes       int64
	Progress        func(BundleProgress)
}

// PackageBundle packages an index and, when supplied, a prepared official genre
// vocabulary. Package and PackageWithProgress remain compatible wrappers for
// index-only bundles.
func PackageBundle(ctx context.Context, options BundlePackageOptions) (m BundleManifest, err error) {
	index, dir, partBytes, progress := options.Index, options.Directory, options.PartBytes, options.Progress
	if partBytes == 0 {
		partBytes = DefaultPartBytes
	}
	if partBytes < 1024 || partBytes > 200_000_000 {
		return m, errors.New("part size must be between 1024 and 200000000 bytes")
	}
	if err = os.Mkdir(dir, 0o700); err != nil {
		entries, readErr := os.ReadDir(dir)
		if !os.IsExist(err) || readErr != nil || len(entries) != 0 {
			return m, fmt.Errorf("choose a new or empty bundle directory: %w", err)
		}
	}
	store, err := Open(index)
	if err != nil {
		return m, err
	}
	info := store.Info()
	_ = store.Close()
	f, err := os.Open(index)
	if err != nil {
		return m, err
	}
	stat, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return m, statErr
	}
	w := &splitWriter{ctx: ctx, dir: dir, limit: partBytes}
	// Better retains most of best's size reduction on indexed SQLite while
	// avoiding its substantially more expensive match search.
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(2), zstd.WithWindowSize(bundleEncoderWindow))
	if err != nil {
		_ = f.Close()
		return m, err
	}
	h := sha256.New()
	report := func(update BundleProgress) {
		if progress != nil {
			progress(BundleProgress{Stage: "hash-index", Done: update.Done, Total: update.Total})
			// The encoder still has pending work when the last input byte is
			// read. Only report compression complete after its output is closed.
			progress(BundleProgress{Stage: "compress-index", Done: min(update.Done, max(int64(0), update.Total-1)), Total: update.Total})
		}
	}
	report(BundleProgress{Total: stat.Size()})
	reader := &byteProgressReader{reader: io.TeeReader(&contextReader{ctx: ctx, r: f}, h), total: stat.Size(), progress: report}
	n, copyErr := io.Copy(zw, reader)
	err = errors.Join(copyErr, f.Close(), zw.Close(), w.Close())
	if err != nil {
		return m, err
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if n != stat.Size() {
		return m, errors.New("MusicBrainz index size changed while packaging")
	}
	m.Index = Artifact{Name: "musicbrainz.sqlite", Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	if progress != nil {
		progress(BundleProgress{Stage: "compress-index", Done: n, Total: n})
	}
	m = BundleManifest{Version: BundleVersion, IndexVersion: IndexVersion, Snapshot: info.Snapshot, CoreLicense: info.CoreLicense, TagsLicense: info.TagsLicense, Index: m.Index, Parts: w.parts}
	if options.GenreVocabulary != "" {
		artifact, copyErr := copyGenreVocabulary(ctx, options.GenreVocabulary, filepath.Join(dir, GenreVocabularyName))
		if copyErr != nil {
			return m, copyErr
		}
		m.GenreVocabulary = &artifact
	}
	if err = m.Validate(); err != nil {
		return m, err
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	err = os.WriteFile(filepath.Join(dir, "musicbrainz-manifest.json"), append(raw, '\n'), 0o600)
	return m, err
}

func copyGenreVocabulary(ctx context.Context, source, target string) (artifact Artifact, err error) {
	if _, err = genrevocab.Load(source); err != nil {
		return artifact, fmt.Errorf("validate genre vocabulary: %w", err)
	}
	in, err := os.Open(source)
	if err != nil {
		return artifact, err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return artifact, err
	}
	keep := false
	defer func() {
		if !keep {
			err = errors.Join(err, removeIfPresent(target))
		}
	}()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(&contextReader{ctx: ctx, r: in}, maxGenreVocabularyBytes+1))
	err = errors.Join(copyErr, out.Sync(), out.Close())
	if err != nil {
		return artifact, err
	}
	if n > maxGenreVocabularyBytes {
		return artifact, errors.New("MusicBrainz genre vocabulary exceeds size limit")
	}
	if _, err = genrevocab.Load(target); err != nil {
		return artifact, fmt.Errorf("validate packaged genre vocabulary: %w", err)
	}
	artifact = Artifact{Name: GenreVocabularyName, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	keep = true
	return artifact, nil
}

type splitWriter struct {
	ctx   context.Context
	dir   string
	limit int64
	file  *os.File
	hash  hash.Hash
	size  int64
	parts []Artifact
}

func (w *splitWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		if w.file == nil {
			if err := w.open(); err != nil {
				return written, err
			}
		}
		n := int(min(int64(len(p)), w.limit-w.size))
		chunk := p[:n]
		count, err := w.file.Write(chunk)
		if count > 0 {
			_, _ = w.hash.Write(chunk[:count])
			w.size += int64(count)
			written += count
			p = p[count:]
		}
		if err != nil {
			return written, err
		}
		if count != n {
			return written, io.ErrShortWrite
		}
		if w.size == w.limit {
			if err := w.finish(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *splitWriter) open() error {
	name := fmt.Sprintf("musicbrainz.sqlite.zst.part-%05d", len(w.parts)+1)
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.file, w.hash, w.size = f, sha256.New(), 0
	return nil
}

func (w *splitWriter) finish() error {
	if w.file == nil {
		return nil
	}
	name := filepath.Base(w.file.Name())
	err := errors.Join(w.file.Sync(), w.file.Close())
	if err == nil {
		w.parts = append(w.parts, Artifact{Name: name, Size: w.size, SHA256: hex.EncodeToString(w.hash.Sum(nil))})
	}
	w.file, w.hash, w.size = nil, nil, 0
	return err
}
func (w *splitWriter) Close() error { return w.finish() }

func VerifyBundle(ctx context.Context, dir string) (BundleManifest, error) {
	return VerifyBundleWithProgress(ctx, dir, nil)
}

func VerifyBundleWithProgress(ctx context.Context, dir string, progress func(BundleProgress)) (BundleManifest, error) {
	var m BundleManifest
	raw, err := os.ReadFile(filepath.Join(dir, "musicbrainz-manifest.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if err = m.Validate(); err != nil {
		return m, err
	}
	paths := make([]string, len(m.Parts))
	var verifiedBase, compressedTotal int64
	for _, part := range m.Parts {
		compressedTotal += part.Size
	}
	for i, part := range m.Parts {
		paths[i] = filepath.Join(dir, part.Name)
		base := verifiedBase
		if err = verifyArtifactProgress(ctx, paths[i], part, func(update BundleProgress) {
			if progress != nil {
				progress(BundleProgress{Stage: "verify-parts", Done: base + update.Done, Total: compressedTotal})
			}
		}); err != nil {
			return m, err
		}
		verifiedBase += part.Size
	}
	tmp, err := os.MkdirTemp("", "playlistai-musicbrainz-verify-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	if err = expandParts(ctx, paths, filepath.Join(tmp, "musicbrainz.sqlite"), m, ports.NopProgress{}, func(done, total int64) {
		if progress != nil {
			progress(BundleProgress{Stage: "verify-index", Done: done, Total: total})
		}
	}); err != nil {
		return m, err
	}
	if m.GenreVocabulary != nil {
		path := filepath.Join(dir, m.GenreVocabulary.Name)
		if err = verifyArtifactProgress(ctx, path, *m.GenreVocabulary, func(update BundleProgress) {
			if progress != nil {
				progress(BundleProgress{Stage: "verify-genres", Done: update.Done, Total: update.Total})
			}
		}); err != nil {
			return m, err
		}
		if _, err = genrevocab.Load(path); err != nil {
			return m, fmt.Errorf("validate MusicBrainz genre vocabulary: %w", err)
		}
	}
	return m, nil
}

func LoadBundle(ctx context.Context, source string) (BundleManifest, error) {
	var m BundleManifest
	u, err := validateBundleURL(source)
	if err != nil {
		return m, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return m, err
	}
	resp, err := bundleClient().Do(req)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return m, fmt.Errorf("HTTP %d loading MusicBrainz manifest", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

func Install(ctx context.Context, source, dir string, p ports.Progress) (installed string, err error) {
	if p == nil {
		p = ports.NopProgress{}
	}
	m, err := LoadBundle(ctx, source)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	lockFile, err := root.OpenFile("install.lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return "", err
	}
	release, err := installlock.TryAcquireFile(lockFile)
	if err != nil {
		return "", fmt.Errorf("MusicBrainz install: %w", err)
	}
	defer func() { err = errors.Join(err, release()) }()
	var indexOwned, genreOwned bool
	installed, indexOwned, err = prepareIndex(ctx, source, dir, m, p)
	if err != nil {
		return "", err
	}
	preparedIndexPath := installed
	genrePath := ""
	activated := false
	defer func() {
		if !activated {
			if indexOwned {
				err = errors.Join(err, removeIfPresent(preparedIndexPath))
			}
			if genreOwned {
				err = errors.Join(err, removeIfPresent(genrePath), removeIfPresent(genrePath+".part"))
			}
		}
	}()
	if m.GenreVocabulary != nil {
		if genrePath, genreOwned, err = prepareGenreVocabulary(ctx, source, dir, *m.GenreVocabulary, p); err != nil {
			return "", err
		}
	}
	oldIndexMarker, oldIndexExists, err := readActivationMarker(dir, "active")
	if err != nil {
		return "", err
	}
	if err = activate(dir, filepath.Base(installed)); err != nil {
		return "", err
	}
	if genrePath != "" {
		if err = activateGenre(dir, filepath.Base(genrePath)); err != nil {
			rollbackErr := restoreActivationMarker(dir, "active", oldIndexMarker, oldIndexExists)
			return "", errors.Join(err, rollbackErr)
		}
	}
	activated = true
	p.Report(ProgressOp, 1, 1, "Offline MusicBrainz metadata ready")
	return installed, nil
}

func prepareIndex(ctx context.Context, source, dir string, m BundleManifest, p ports.Progress) (installed string, owned bool, err error) {
	target := filepath.Join(dir, "musicbrainz-"+strings.ToLower(m.Index.SHA256)+".sqlite")
	// Repairs may use a fresh sibling when the hash-named file is corrupt and
	// still open by another reader. Reuse an already repaired active copy too.
	for _, candidate := range []string{target, ActivePath(dir)} {
		if verifyArtifact(ctx, candidate, m.Index) == nil {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			return candidate, false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	// Never unlink or overwrite a mapped corrupt database. Always own a fresh
	// target, including the first install, so cancellation cleanup cannot delete
	// a file created by another writer after our initial validation.
	fresh, err := os.CreateTemp(dir, "musicbrainz-"+strings.ToLower(m.Index.SHA256)+"-*.sqlite")
	if err != nil {
		return "", false, err
	}
	target = fresh.Name()
	if err := fresh.Close(); err != nil {
		_ = os.Remove(target)
		return "", false, err
	}
	prepared := false
	defer func() {
		if !prepared {
			err = errors.Join(err, removeIfPresent(target))
		}
	}()
	u, _ := validateBundleURL(source)
	paths := make([]string, len(m.Parts))
	total := int64(0)
	for _, part := range m.Parts {
		total += part.Size
	}
	doneBase := int64(0)
	client := bundleClient()
	for i, part := range m.Parts {
		remote := u.ResolveReference(&url.URL{Path: part.Name}).String()
		path := filepath.Join(dir, strings.ToLower(part.SHA256)+".part")
		base := doneBase
		if verifyArtifact(ctx, path, part) != nil {
			_, err = dataset.DownloadWithClient(ctx, remote, path, part.Size, part.SHA256, func(done, _ int64) {
				p.Report(ProgressOp, base+done, total, fmt.Sprintf("Downloading MusicBrainz data part %d of %d", i+1, len(m.Parts)))
			}, client)
			if err != nil {
				return "", false, err
			}
		}
		paths[i] = path
		doneBase += part.Size
	}
	if err = expandParts(ctx, paths, target, m, p, nil); err != nil {
		return "", false, err
	}
	if err = ctx.Err(); err != nil {
		return "", false, err
	}
	prepared = true
	for _, path := range paths {
		_ = os.Remove(path)
	}
	return target, true, nil
}

func prepareGenreVocabulary(ctx context.Context, source, dir string, artifact Artifact, p ports.Progress) (preparedPath string, owned bool, err error) {
	hash := strings.ToLower(artifact.SHA256)
	deterministic := filepath.Join(dir, "musicbrainz-genres-"+hash+".json")
	for _, candidate := range []string{deterministic, ActiveGenrePath(dir)} {
		if validateGenreVocabularyFile(ctx, candidate, artifact) == nil {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			return candidate, false, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	stage, err := os.CreateTemp(dir, "musicbrainz-genres-"+hash+"-*.json")
	if err != nil {
		return "", false, err
	}
	target := stage.Name()
	if err = stage.Close(); err != nil {
		_ = os.Remove(target)
		return "", false, err
	}
	if err = os.Remove(target); err != nil {
		return "", false, err
	}
	prepared := false
	defer func() {
		if !prepared {
			err = errors.Join(err, removeIfPresent(target), removeIfPresent(target+".part"))
		}
	}()
	u, _ := validateBundleURL(source)
	remote := u.ResolveReference(&url.URL{Path: artifact.Name}).String()
	_, err = dataset.DownloadWithClient(ctx, remote, target, artifact.Size, artifact.SHA256, func(done, total int64) {
		p.Report(ProgressOp, done, total, "Downloading MusicBrainz genre vocabulary")
	}, bundleClient())
	if err != nil {
		return "", false, err
	}
	if err = validateGenreVocabularyFile(ctx, target, artifact); err != nil {
		return "", false, err
	}
	if err = ctx.Err(); err != nil {
		return "", false, err
	}
	prepared = true
	return target, true, nil
}

func readActivationMarker(dir, marker string) (string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, marker))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(raw)), true, nil
}

func restoreActivationMarker(dir, marker, value string, existed bool) error {
	if !existed {
		return removeIfPresent(filepath.Join(dir, marker))
	}
	return activateMarker(dir, marker, value)
}

func validateGenreVocabularyFile(ctx context.Context, path string, artifact Artifact) error {
	if err := verifyArtifact(ctx, path, artifact); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := genrevocab.Load(path)
	return err
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func expandParts(ctx context.Context, paths []string, target string, m BundleManifest, p ports.Progress, progress func(done, total int64)) error {
	files := make([]*os.File, 0, len(paths))
	readers := make([]io.Reader, 0, len(paths))
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	zr, err := zstd.NewReader(&contextReader{ctx: ctx, r: io.MultiReader(readers...)}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(bundleWindow))
	if err != nil {
		return err
	}
	defer zr.Close()
	out, err := os.CreateTemp(filepath.Dir(target), ".musicbrainz-unpack-*.sqlite")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	p.Report(ProgressOp, 0, m.Index.Size, "Decompressing and verifying MusicBrainz data")
	h := sha256.New()
	writer := &byteProgressWriter{writer: io.MultiWriter(out, h), total: m.Index.Size, progress: func(done, total int64) {
		p.Report(ProgressOp, done, total, "Decompressing and verifying MusicBrainz data")
		if progress != nil {
			progress(done, total)
		}
	}}
	n, err := io.Copy(writer, io.LimitReader(zr, m.Index.Size+1))
	if err != nil {
		out.Close()
		return err
	}
	if n != m.Index.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.Index.SHA256) {
		out.Close()
		return errors.New("MusicBrainz index size/checksum mismatch")
	}
	if err = errors.Join(out.Sync(), out.Close()); err != nil {
		return err
	}
	store, err := Open(tmp)
	if err != nil {
		return err
	}
	info := store.Info()
	_ = store.Close()
	if info.Version != m.IndexVersion || info.Snapshot != m.Snapshot {
		return errors.New("MusicBrainz index does not match manifest")
	}
	return os.Rename(tmp, target)
}

func fileArtifactProgress(ctx context.Context, path, stage string, progress func(BundleProgress)) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return Artifact{}, err
	}
	h := sha256.New()
	reader := &byteProgressReader{reader: &contextReader{ctx: ctx, r: f}, total: stat.Size(), stage: stage, progress: progress}
	n, err := io.Copy(h, reader)
	return Artifact{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}
func verifyArtifact(ctx context.Context, path string, a Artifact) error {
	return verifyArtifactProgress(ctx, path, a, nil)
}
func verifyArtifactProgress(ctx context.Context, path string, a Artifact, progress func(BundleProgress)) error {
	got, err := fileArtifactProgress(ctx, path, "verify-parts", progress)
	if err != nil {
		return err
	}
	if got.Size != a.Size || !strings.EqualFold(got.SHA256, a.SHA256) {
		return errors.New("MusicBrainz artifact verification failed")
	}
	return nil
}

type byteProgressReader struct {
	reader   io.Reader
	done     int64
	reported int64
	total    int64
	stage    string
	progress func(BundleProgress)
}

func (r *byteProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.done += int64(n)
	if r.progress != nil && (r.done-r.reported >= 8<<20 || r.done >= r.total || errors.Is(err, io.EOF)) {
		r.progress(BundleProgress{Stage: r.stage, Done: min(r.done, r.total), Total: r.total})
		r.reported = r.done
	}
	return n, err
}

type byteProgressWriter struct {
	writer   io.Writer
	done     int64
	reported int64
	total    int64
	progress func(done, total int64)
}

func (w *byteProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.done += int64(n)
	if w.progress != nil && n > 0 && (w.done-w.reported >= 8<<20 || w.done >= w.total) {
		w.progress(min(w.done, w.total), w.total)
		w.reported = w.done
	}
	return n, err
}
func activate(dir, name string) error {
	return activateMarker(dir, "active", name)
}

func activateGenre(dir, name string) error {
	return activateMarker(dir, "active-genres", name)
}

func activateMarker(dir, marker, name string) error {
	tmp, err := os.CreateTemp(dir, ".active-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err = io.WriteString(tmp, name); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, marker))
}
func ActivePath(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "active"))
	name := strings.TrimSpace(string(raw))
	if err == nil && filepath.Base(name) == name && strings.HasPrefix(name, "musicbrainz-") && strings.HasSuffix(name, ".sqlite") && !strings.ContainsAny(name, `/\`) {
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, "musicbrainz.sqlite")
}

// ActiveGenrePath returns the atomically selected optional genre vocabulary.
// The fallback path supports manually installed vocabularies and may not exist.
func ActiveGenrePath(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "active-genres"))
	name := strings.TrimSpace(string(raw))
	if err == nil && validActiveGenreName(name) {
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, GenreVocabularyName)
}

// ActiveGenreHash returns the bundle artifact hash encoded in the active genre
// filename, or an empty string when no managed vocabulary is active.
func ActiveGenreHash(dir string) string {
	name := filepath.Base(ActiveGenrePath(dir))
	const prefix = "musicbrainz-genres-"
	if !validActiveGenreName(name) || len(name) < len(prefix)+64 {
		return ""
	}
	hash := name[len(prefix) : len(prefix)+64]
	if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return strings.ToLower(hash)
}

func validActiveGenreName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, "musicbrainz-genres-") && strings.HasSuffix(name, ".json") && !strings.ContainsAny(name, `/\\`)
}
func validateBundleURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	loop := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && (u.Scheme != "http" || !loop) {
		return nil, errors.New("MusicBrainz bundle source must use HTTPS")
	}
	return u, nil
}

func bundleClient() *http.Client {
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many MusicBrainz bundle redirects")
		}
		_, err := validateBundleURL(req.URL.String())
		return err
	}}
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
