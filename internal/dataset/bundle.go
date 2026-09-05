package dataset

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/ports"
)

// BundleOp is the progress op label for local-archive decompression. It's the
// same label Fetch uses (ProgressOp) so the frontend's one "catalog" progress
// listener (see frontend/src/components/useProgress.ts) covers both the
// network-download path and this local-unpack path without change.
const BundleOp = ProgressOp

// bundleArchiveName is the file every packaging target stages next to the app
// binary — see cmd/catalogpack and docs/CATALOG.md.
const bundleArchiveName = "catalog.tar.zst"

const manifestEntryName = "catalog-manifest.json"

// FindBundledArchive looks for a pre-packaged, compressed catalog next to the
// running executable — the same place internal/intent/llama's resolveBinary
// looks for llama-server, and where every packaging target
// (build/Taskfile.yml's stage:catalog) stages catalog.tar.zst when a local
// catalog build was available at package time (see cmd/catalogpack).
// explicit, when non-empty (config: catalog.bundle_path), is checked instead.
//
// A present-but-empty file doesn't count as bundled: packaging always stages
// bin/catalog.tar.zst (nfpm/NSIS/the .app bundle reference it as a fixed
// path), writing a 0-byte placeholder when no local catalog build was
// available — this is how that "nothing to bundle" case reaches here.
func FindBundledArchive(explicit string) (string, bool) {
	if explicit != "" {
		fi, err := os.Stat(explicit)
		return explicit, err == nil && !fi.IsDir() && fi.Size() > 0
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	cand := filepath.Join(filepath.Dir(exe), bundleArchiveName)
	fi, err := os.Stat(cand)
	return cand, err == nil && !fi.IsDir() && fi.Size() > 0
}

// Unpack decompresses a catalog.tar.zst archive (written by cmd/catalogpack)
// into dir, verifying each extracted file's size + SHA-256 against the
// catalog-manifest.json entry embedded in the archive before promoting it
// (via the same target+".part" -> rename dance Download uses). Progress is
// reported under BundleOp as bytes of the *compressed* archive consumed —
// the same approximation Fetch makes for downloads (proportional, not exact,
// but monotonic and cheap).
func Unpack(ctx context.Context, archivePath, dir string, p ports.Progress) error {
	if p == nil {
		p = ports.NopProgress{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	fi, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}
	total := fi.Size()

	f, err := os.Open(archivePath) //nolint:gosec // path from FindBundledArchive / operator config
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}
	defer f.Close()

	cr := &countingReader{r: f, onRead: func(n int64) {
		p.Report(BundleOp, n, total, "Decompressing dataset")
	}}

	zr, err := zstd.NewReader(cr)
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}
	defer zr.Close()

	var m *Manifest
	extracted := make(map[string]string) // manifest file name -> ".part" path written

	// Whatever happens, never leave a ".part" file behind: either every
	// extracted file gets verified + promoted (success=true below suppresses
	// this), or none of them do.
	success := false
	defer func() {
		if success {
			return
		}
		for _, part := range extracted {
			os.Remove(part) //nolint:errcheck
		}
	}()

	tr := tar.NewReader(zr)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		name := filepath.Base(hdr.Name) // defend against any directory components

		if name == manifestEntryName {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("unpack: read manifest entry: %w", err)
			}
			var mm Manifest
			if err := json.Unmarshal(raw, &mm); err != nil {
				return fmt.Errorf("unpack: parse manifest entry: %w", err)
			}
			m = &mm
			continue
		}

		if m == nil || !m.has(name) {
			return fmt.Errorf("unpack: %s: not listed in the archive's manifest", name)
		}
		target := filepath.Join(dir, name+partSuffix)
		out, err := os.Create(target) //nolint:gosec
		if err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		_, cerr := io.Copy(out, tr) //nolint:gosec // tar entry size bounded by the manifest check below
		if err := out.Close(); err != nil && cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return fmt.Errorf("unpack %s: %w", name, cerr)
		}
		extracted[name] = target
	}

	if m == nil {
		return fmt.Errorf("unpack: archive has no %s entry", manifestEntryName)
	}

	for _, want := range m.Files {
		part, ok := extracted[want.Name]
		if !ok {
			return fmt.Errorf("unpack: archive's manifest lists %s but the archive had no such entry", want.Name)
		}
		if err := verifyFile(part, want.Size, want.SHA256); err != nil {
			return fmt.Errorf("unpack: %s: %w", want.Name, err)
		}
	}
	for _, want := range m.Files {
		if err := os.Rename(extracted[want.Name], filepath.Join(dir, want.Name)); err != nil {
			return fmt.Errorf("unpack: %s: %w", want.Name, err)
		}
	}

	success = true
	p.Report(BundleOp, total, total, "ready")
	return nil
}

// has reports whether name is one of m's listed files.
func (m *Manifest) has(name string) bool {
	for _, f := range m.Files {
		if f.Name == name {
			return true
		}
	}
	return false
}

type countingReader struct {
	r      io.Reader
	n      int64
	onRead func(n int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		if c.onRead != nil {
			c.onRead(c.n)
		}
	}
	return n, err
}
