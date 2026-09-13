// Package modelpack installs checksummed, segmented tar.zst model bundles.
package modelpack

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
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/ports"
)

// MaxPartBytes is the exclusive hosting upload limit.
const MaxPartBytes int64 = 200000000
const maxManifestBytes = 4 << 20
const maxPackBytes int64 = 64 << 30
const maxFileBytes int64 = 16 << 30

// Manifest describes ordered fragments of one tar.zst stream and its files.
type Manifest struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Parts   []Part `json:"parts"`
	Files   []File `json:"files"`
}

// Part is a relative path or HTTPS URL and the exact compressed bytes.
type Part struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// File describes an extracted regular file, relative to the destination.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func safePath(s string) bool {
	if s == "" || s == "." || path.Clean(s) != s || strings.ContainsAny(s, "\\:\x00") || strings.HasPrefix(s, "/") || s == ".." || strings.HasPrefix(s, "../") {
		return false
	}
	for _, component := range strings.Split(s, "/") {
		if strings.TrimRight(component, " .") != component {
			return false
		}
		base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}
	return true
}
func validHash(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == sha256.Size }
func httpsURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func downloadClient(timeout time.Duration) *http.Client {
	client := *http.DefaultClient
	client.Timeout = timeout
	previous := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !httpsURL(req.URL.String()) {
			return errors.New("unsafe model pack redirect")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	return &client
}

// Validate checks schema, portable names and finite size limits before any writes.
func (m Manifest) Validate() error {
	if m.Version != 1 || m.Name == "" || len(m.Parts) == 0 || len(m.Parts) > 10000 || len(m.Files) == 0 || len(m.Files) > 10000 {
		return errors.New("invalid model pack manifest")
	}
	seen := map[string]bool{}
	var total int64
	for _, p := range m.Parts {
		if (!safePath(p.Path) && !httpsURL(p.Path)) || p.Size <= 0 || p.Size >= MaxPartBytes || !validHash(p.SHA256) || seen[p.Path] {
			return errors.New("invalid model pack part")
		}
		seen[p.Path] = true
		total += p.Size
		if total > maxPackBytes {
			return errors.New("model pack compressed size exceeds limit")
		}
	}
	seen = map[string]bool{}
	total = 0
	for _, f := range m.Files {
		key := strings.ToLower(f.Path)
		if !safePath(f.Path) || f.Size < 0 || f.Size > maxFileBytes || !validHash(f.SHA256) || seen[key] {
			return errors.New("invalid model pack file")
		}
		seen[key] = true
		total += f.Size
		if total > maxPackBytes {
			return errors.New("model pack extracted size exceeds limit")
		}
	}
	for name := range seen {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return errors.New("conflicting model pack file paths")
			}
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

// ReadManifest reads a bounded local JSON file or HTTPS manifest.
func ReadManifest(ctx context.Context, location string) (Manifest, error) {
	var m Manifest
	if e := ctx.Err(); e != nil {
		return m, e
	}
	var r io.ReadCloser
	if httpsURL(location) {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if e != nil {
			return m, e
		}
		resp, e := downloadClient(60 * time.Second).Do(req)
		if e != nil {
			return m, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return m, fmt.Errorf("model manifest HTTP %d", resp.StatusCode)
		}
		r = resp.Body
	} else {
		if strings.Contains(location, "://") {
			return m, errors.New("model manifests require HTTPS")
		}
		f, e := os.Open(location)
		if e != nil {
			return m, e
		}
		defer f.Close()
		r = f
	}
	b, e := io.ReadAll(io.LimitReader(contextReader{ctx, r}, maxManifestBytes+1))
	if e != nil {
		return m, e
	}
	if len(b) > maxManifestBytes {
		return m, errors.New("model manifest too large")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	return m, m.Validate()
}

func verify(ctx context.Context, filename string, size int64, sum string) error {
	f, e := os.Open(filename)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("model file size mismatch")
	}
	h := sha256.New()
	if _, e = io.Copy(h, contextReader{ctx, f}); e != nil {
		return e
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sum) {
		return errors.New("model file checksum mismatch")
	}
	return nil
}

// Fetch verifies or resumes parts in cacheDir and installs into a new destination.
// Existing destinations are never replaced. Callers validate and activate the new
// directory through their existing model-specific activation path.
func Fetch(ctx context.Context, manifestLocation, cacheDir, destination string, p ports.Progress) error {
	m, e := ReadManifest(ctx, manifestLocation)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(destination); !os.IsNotExist(e) {
		if e != nil {
			return e
		}
		return errors.New("model destination already exists")
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	if e = os.MkdirAll(cacheDir, 0o755); e != nil {
		return e
	}
	var total, done int64
	for _, part := range m.Parts {
		total += part.Size
	}
	names := make([]string, 0, len(m.Parts))
	for _, part := range m.Parts {
		target := filepath.Join(cacheDir, strings.ToLower(part.SHA256)+".partdata")
		if e = verify(ctx, target, part.Size, part.SHA256); e != nil {
			source := part.Path
			if !httpsURL(source) {
				if httpsURL(manifestLocation) {
					base, _ := url.Parse(manifestLocation)
					relative := &url.URL{Path: part.Path}
					source = base.ResolveReference(relative).String()
				} else {
					source = filepath.Join(filepath.Dir(manifestLocation), filepath.FromSlash(part.Path))
				}
			}
			if httpsURL(source) {
				_, e = dataset.DownloadWithClient(ctx, source, target, part.Size, part.SHA256, func(n, _ int64) { p.Report("modelpack", done+n, total, part.Path) }, downloadClient(30*time.Minute))
			} else {
				e = copyLocal(ctx, source, target, part.Size, part.SHA256)
			}
			if e != nil {
				return fmt.Errorf("model part %s: %w", part.Path, e)
			}
		}
		done += part.Size
		p.Report("modelpack", done, total, part.Path)
		names = append(names, target)
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0o755); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".modelpack-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	if e = extract(ctx, names, stage, m.Files); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if _, e = os.Lstat(destination); !os.IsNotExist(e) {
		return errors.New("model destination appeared during installation")
	}
	return os.Rename(stage, destination)
}

func copyLocal(ctx context.Context, source, target string, size int64, sum string) error {
	if e := verify(ctx, source, size, sum); e != nil {
		return e
	}
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.CreateTemp(filepath.Dir(target), ".modelpart-")
	if e != nil {
		return e
	}
	name := out.Name()
	defer os.Remove(name)
	_, e = io.Copy(out, io.LimitReader(contextReader{ctx, in}, size+1))
	closeErr := out.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = verify(ctx, name, size, sum); e != nil {
		return e
	}
	return os.Rename(name, target)
}

// partReader keeps only one segment open, even for large packs.
type partReader struct {
	names   []string
	current *os.File
}

func (r *partReader) Close() error {
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}
func (r *partReader) Read(p []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.names) == 0 {
				return 0, io.EOF
			}
			f, e := os.Open(r.names[0])
			if e != nil {
				return 0, e
			}
			r.current = f
			r.names = r.names[1:]
		}
		n, e := r.current.Read(p)
		if errors.Is(e, io.EOF) {
			if ce := r.current.Close(); ce != nil {
				return n, ce
			}
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, e
	}
}

func extract(ctx context.Context, names []string, destination string, files []File) error {
	reader := &partReader{names: names}
	defer reader.Close()
	decoder, e := zstd.NewReader(contextReader{ctx, reader}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
	if e != nil {
		return e
	}
	defer decoder.Close()
	tr := tar.NewReader(contextReader{ctx, decoder})
	expected := map[string]File{}
	for _, f := range files {
		expected[f.Path] = f
	}
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		f, ok := expected[h.Name]
		if !ok || seen[h.Name] || !safePath(h.Name) || h.Typeflag != tar.TypeReg || h.Size != f.Size {
			return fmt.Errorf("invalid model archive entry %q", h.Name)
		}
		seen[h.Name] = true
		target := filepath.Join(destination, filepath.FromSlash(h.Name))
		if e = os.MkdirAll(filepath.Dir(target), 0o755); e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, hash), contextReader{ctx, tr})
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != f.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), f.SHA256) {
			return fmt.Errorf("model archive checksum mismatch: %s", h.Name)
		}
	}
	if len(seen) != len(expected) {
		return errors.New("model archive missing files")
	}
	// Force the zstd checksum/trailer to be read; reject additional payload after tar EOF.
	var trailing [4096]byte
	var trailingBytes int64
	for {
		n, e := decoder.Read(trailing[:])
		trailingBytes += int64(n)
		if trailingBytes > 1<<20 {
			return errors.New("excess model archive padding")
		}
		for _, b := range trailing[:n] {
			if b != 0 {
				return errors.New("unexpected payload after model archive")
			}
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
	return ctx.Err()
}
