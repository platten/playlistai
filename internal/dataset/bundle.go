package dataset

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
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

// DownloadArchive fetches a compressed catalog (catalog.tar.zst) from url into
// target, resuming a partial download via HTTP range and verifying size +
// SHA-256 when given (pass 0 / "" to skip). Progress is reported under
// BundleOp as bytes downloaded. Skips the download entirely when target is
// already present and valid.
func DownloadArchive(ctx context.Context, url, target string, size int64, sha256hex string, p ports.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if verifyFileContext(ctx, target, size, sha256hex) == nil {
		if size > 0 {
			p.Report(BundleOp, size, size, "have catalog archive")
		}
		return nil
	}
	p.Report(BundleOp, 0, size, "downloading dataset")
	_, err := Download(ctx, url, target, size, sha256hex, func(done, total int64) {
		p.Report(BundleOp, done, total, "downloading dataset")
	})
	return err
}

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
// as one recoverable catalog transaction. Progress is
// reported under BundleOp as bytes of the *compressed* archive consumed —
// the same approximation Fetch makes for downloads (proportional, not exact,
// but monotonic and cheap).
func Unpack(ctx context.Context, archivePath, dir string, p ports.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	if err := Recover(dir); err != nil {
		return err
	}

	// Already unpacked? If dir holds a catalog whose files match a
	// catalog-manifest.json sitting next to it, there is nothing to do.
	if m, err := LoadManifest(ctx, filepath.Join(dir, manifestEntryName)); err == nil && allPresentContext(ctx, dir, m) {
		p.Report(BundleOp, 1, 1, "ready")
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := beginCatalogInstall(dir)
	if err != nil {
		return err
	}
	defer c.close()
	// Another installer may have completed while this call acquired the lock.
	if m, err := LoadManifest(ctx, filepath.Join(dir, manifestEntryName)); err == nil && allPresentContext(ctx, dir, m) {
		p.Report(BundleOp, 1, 1, "ready")
		return nil
	}
	if err := c.prepare(); err != nil {
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

	zr, err := zstd.NewReader(contextReader{ctx, cr}, zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(128<<20))
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}
	defer zr.Close()

	var m *Manifest
	extracted := make(map[string]bool)
	declared := make(map[string]File)

	tr := tar.NewReader(zr)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		name := hdr.Name
		if !validArtifactName(name) || hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("unpack: invalid regular-file entry %q", name)
		}

		if name == manifestEntryName {
			if m != nil || hdr.Size < 0 || hdr.Size > maxManifestBytes {
				return fmt.Errorf("unpack: invalid manifest entry")
			}
			raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, tr}, maxManifestBytes+1))
			if err != nil {
				return fmt.Errorf("unpack: read manifest entry: %w", err)
			}
			var mm Manifest
			if err := json.Unmarshal(raw, &mm); err != nil {
				return fmt.Errorf("unpack: parse manifest entry: %w", err)
			}
			if err := mm.Validate(); err != nil {
				return fmt.Errorf("unpack: %w", err)
			}
			m = &mm
			for _, f := range m.Files {
				declared[f.Name] = f
			}
			if err := writeSynced(c.root, filepath.Join(c.stage, manifestEntryName), raw); err != nil {
				return err
			}
			continue
		}

		want, listed := declared[name]
		if !listed || extracted[name] {
			return fmt.Errorf("unpack: %s: not listed in the archive's manifest", name)
		}
		if hdr.Size != want.Size {
			return fmt.Errorf("unpack: %s: header size does not match manifest", name)
		}
		target := filepath.Join(c.stage, name)
		out, err := c.root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		extracted[name] = true // the entire owned stage is cleaned even on a short copy
		n, cerr := io.Copy(out, io.LimitReader(contextReader{ctx, tr}, want.Size))
		if cerr == nil && n != want.Size {
			cerr = io.ErrUnexpectedEOF
		}
		if cerr == nil {
			cerr = out.Sync()
		}
		if err := out.Close(); err != nil && cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return fmt.Errorf("unpack %s: %w", name, cerr)
		}
	}

	if m == nil {
		return fmt.Errorf("unpack: archive has no %s entry", manifestEntryName)
	}

	for _, want := range m.Files {
		if !extracted[want.Name] {
			return fmt.Errorf("unpack: archive's manifest lists %s but the archive had no such entry", want.Name)
		}
		if err := verifyFileContext(ctx, filepath.Join(dir, c.stage, want.Name), want.Size, want.SHA256); err != nil {
			return fmt.Errorf("unpack: %s: %w", want.Name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	names := make([]string, 0, len(m.Files)+1)
	for _, want := range m.Files {
		names = append(names, want.Name)
	}
	if err := c.publish(append(names, manifestEntryName)); err != nil {
		return fmt.Errorf("unpack: activate catalog: %w", err)
	}
	p.Report(BundleOp, total, total, "ready")
	return nil
}

// allPresent reports whether every file m lists already exists in dir with the
// right size + SHA-256.
func allPresentContext(ctx context.Context, dir string, m *Manifest) bool {
	complete, _ := StatusContext(ctx, dir, m)
	return complete
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
