// Command catalogpack compresses a converted catalog directory
// (vectors.i8 + catalog.sqlite + catalog-manifest.json, produced by
// python/convert_pickles.py) into a single catalog.tar.zst archive that
// internal/dataset.Unpack decompresses on first launch. It's maintainer/build
// tooling invoked by the packaging Taskfiles — it is not part of the shipped
// app and is skipped by them entirely when no local catalog build exists.
//
// The archive holds three tar entries: catalog-manifest.json first (so the
// decoder always has it before any data file), then vectors.i8 and
// catalog.sqlite in the order the manifest lists them. Compression is
// klauspost/compress's zstd at SpeedBestCompression — the highest ratio that
// pure-Go implementation offers (chosen over shelling out to a system zstd
// binary so packaging stays portable across the CI build matrix).
//
// Usage:
//
//	go run ./cmd/catalogpack -in build/catalog -out build/catalog-dist/catalog.tar.zst
//
// build/catalog-dist/ is git-ignored. Upload the resulting catalog.tar.zst to
// wherever config.Default() points catalog.archive_url (currently Google
// Drive) and update its pinned size + sha256, then re-run when the underlying
// dataset changes. See docs/CATALOG.md.
package main

import (
	"archive/tar"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

type manifestFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Files []manifestFile `json:"files"`
}

func main() {
	in := flag.String("in", "build/catalog", "directory holding vectors.i8, catalog.sqlite, catalog-manifest.json")
	out := flag.String("out", "build/catalog-dist/catalog.tar.zst", "output archive path (git-ignored; upload to catalog.archive_url's host)")
	flag.Parse()

	if err := run(*in, *out); err != nil {
		log.Fatal(err)
	}
}

func run(inDir, outPath string) error {
	manifestPath := filepath.Join(inDir, "catalog-manifest.json")
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // maintainer-supplied build path
	if err != nil {
		return fmt.Errorf("read %s: %w (run python/convert_pickles.py first)", manifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("%s lists no files", manifestPath)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	tmp := outPath + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec
	if err != nil {
		return err
	}
	defer os.Remove(tmp) //nolint:errcheck // no-op once the rename below succeeds

	zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		f.Close() //nolint:errcheck,gosec
		return err
	}
	tw := tar.NewWriter(zw)

	if err := writeTarEntry(tw, "catalog-manifest.json", raw); err != nil {
		return fmt.Errorf("write manifest entry: %w", err)
	}

	var totalIn int64
	for _, entry := range m.Files {
		src := filepath.Join(inDir, entry.Name)
		fi, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		if fi.Size() != entry.Size {
			return fmt.Errorf("%s: manifest says %d bytes, file is %d", entry.Name, entry.Size, fi.Size())
		}
		if err := tw.WriteHeader(&tar.Header{Name: entry.Name, Size: fi.Size(), Mode: 0o644}); err != nil {
			return fmt.Errorf("write %s header: %w", entry.Name, err)
		}
		in, err := os.Open(src) //nolint:gosec
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, in)
		in.Close() //nolint:errcheck,gosec
		if err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name, err)
		}
		totalIn += n
		fmt.Fprintf(os.Stderr, "  %-20s %14d bytes\n", entry.Name, n)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd: %w", err)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fi, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	totalOut := fi.Size()

	if err := os.Rename(tmp, outPath); err != nil {
		return err
	}

	ratio := 100 * float64(totalOut) / float64(totalIn)
	fmt.Fprintf(os.Stderr, "wrote %s: %d -> %d bytes (%.1f%%)\n", outPath, totalIn, totalOut, ratio)
	return nil
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
