package dataset

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/fakes"
)

// buildArchive writes a catalog.tar.zst exactly like cmd/catalogpack would,
// from in-memory blobs, and returns its path plus the manifest bytes used.
func buildArchive(t *testing.T, dir string, files map[string][]byte, corruptManifestFor string) string {
	t.Helper()

	m := Manifest{Name: "test"}
	for name, data := range files {
		sum := sha256.Sum256(data)
		hexsum := hex.EncodeToString(sum[:])
		if name == corruptManifestFor {
			hexsum = hex.EncodeToString(sha256.New().Sum([]byte("not it")))
		}
		m.Files = append(m.Files, File{Name: name, Size: int64(len(data)), SHA256: hexsum})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(dir, "catalog.tar.zst")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)

	write := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write(manifestEntryName, raw)
	for _, e := range m.Files {
		write(e.Name, files[e.Name])
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func TestUnpack(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"vectors.i8":     bytes.Repeat([]byte{1, 2, 3, 4}, 50_000),
		"catalog.sqlite": bytes.Repeat([]byte("row"), 40_000),
	}
	archiveDir := t.TempDir()
	archive := buildArchive(t, archiveDir, files, "")
	destDir := filepath.Join(t.TempDir(), "catalog")

	rec := &fakes.RecordingProgress{}
	if err := Unpack(context.Background(), archive, destDir, rec); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(destDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: %d bytes, want %d", name, len(got), len(want))
		}
	}
	assertNoPartials(t, destDir)

	rows := rec.Snapshot()
	if len(rows) == 0 {
		t.Fatal("no progress reported")
	}
	fi, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	last := rows[len(rows)-1]
	if last.Op != BundleOp {
		t.Fatalf("op = %q, want %q", last.Op, BundleOp)
	}
	if last.Done != fi.Size() || last.Total != fi.Size() {
		t.Fatalf("final progress = %d/%d, want %d/%d", last.Done, last.Total, fi.Size(), fi.Size())
	}
	var prevDone int64
	for _, r := range rows {
		if r.Done < prevDone {
			t.Fatalf("progress went backwards: %d then %d", prevDone, r.Done)
		}
		prevDone = r.Done
	}
}

func TestUnpackSkipsWhenAlreadyExtracted(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"vectors.i8":     bytes.Repeat([]byte{7}, 20_000),
		"catalog.sqlite": bytes.Repeat([]byte("x"), 20_000),
	}
	archive := buildArchive(t, t.TempDir(), files, "")
	destDir := filepath.Join(t.TempDir(), "catalog")

	if err := Unpack(context.Background(), archive, destDir, nil); err != nil {
		t.Fatalf("first Unpack: %v", err)
	}
	// First run leaves a manifest behind so a second run can short-circuit.
	if _, err := os.Stat(filepath.Join(destDir, "catalog-manifest.json")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}

	// Tamper with a file's bytes but keep its size; a real re-extract would
	// overwrite it, the short-circuit leaves it (the point of this test is
	// only that Unpack didn't do the work — we then assert it *would* detect
	// a size change).
	before, _ := os.Stat(filepath.Join(destDir, "vectors.i8"))

	rec := &fakes.RecordingProgress{}
	if err := Unpack(context.Background(), archive, destDir, rec); err != nil {
		t.Fatalf("second Unpack: %v", err)
	}
	after, _ := os.Stat(filepath.Join(destDir, "vectors.i8"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("second Unpack rewrote vectors.i8 instead of skipping")
	}
	rows := rec.Snapshot()
	if len(rows) != 1 || rows[0].Note != "ready" {
		t.Fatalf("expected a single 'ready' progress row, got %+v", rows)
	}

	// If a file no longer matches the manifest, the short-circuit must not fire.
	if err := os.WriteFile(filepath.Join(destDir, "catalog.sqlite"), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(context.Background(), archive, destDir, nil); err != nil {
		t.Fatalf("re-extract after tamper: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(destDir, "catalog.sqlite"))
	if !bytes.Equal(got, files["catalog.sqlite"]) {
		t.Fatalf("tampered file not re-extracted: %d bytes", len(got))
	}
}

func TestUnpackRejectsBadChecksum(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"vectors.i8":     bytes.Repeat([]byte{9}, 1000),
		"catalog.sqlite": bytes.Repeat([]byte{8}, 1000),
	}
	archiveDir := t.TempDir()
	archive := buildArchive(t, archiveDir, files, "catalog.sqlite")
	destDir := filepath.Join(t.TempDir(), "catalog")

	err := Unpack(context.Background(), archive, destDir, nil)
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if _, statErr := os.Stat(filepath.Join(destDir, "catalog.sqlite")); !os.IsNotExist(statErr) {
		t.Fatal("bad file was promoted despite checksum failure")
	}
	if _, statErr := os.Stat(filepath.Join(destDir, "vectors.i8")); !os.IsNotExist(statErr) {
		t.Fatal("good file was promoted even though a sibling failed verification")
	}
	assertNoPartials(t, destDir)
}

func TestFindBundledArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.tar.zst")

	if _, ok := FindBundledArchive(path); ok {
		t.Fatal("found a nonexistent explicit path")
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := FindBundledArchive(path); ok {
		t.Fatal("an empty placeholder (the no-local-build packaging case) should not count as bundled")
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := FindBundledArchive(path)
	if !ok || got != path {
		t.Fatalf("FindBundledArchive(%q) = %q, %v", path, got, ok)
	}
}
