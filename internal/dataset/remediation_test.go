package dataset

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/installlock"
)

func TestReadLeasesExcludeActivationUntilLastReaderCloses(t *testing.T) {
	dir := t.TempDir()
	first, err := ReadLease(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first() }()
	second, err := ReadLease(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second() }()
	assertBusy := func() {
		t.Helper()
		c, err := beginCatalogInstall(dir)
		if err == nil {
			c.close()
			t.Fatal("activation accepted while reader remains")
		}
		if !errors.Is(err, installlock.ErrBusy) {
			t.Fatal(err)
		}
	}
	assertBusy()
	if err := first(); err != nil {
		t.Fatal(err)
	}
	assertBusy()
	if err := second(); err != nil {
		t.Fatal(err)
	}
	c, err := beginCatalogInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if release, err := ReadLease(dir); !errors.Is(err, installlock.ErrBusy) {
		if release != nil {
			_ = release()
		}
		t.Fatalf("reader entered installation: %v", err)
	}
}

func TestRecoverCleansOwnedStagesWithoutJournal(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			dir := t.TempDir()
			c, err := beginCatalogInstall(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.prepare(); err != nil {
				t.Fatal(err)
			}
			if err := writeSynced(c.root, filepath.Join(c.stage, "a"), []byte("new")); err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := c.publish([]string{"a"}); err != nil {
					t.Fatal(err)
				}
			}
			c.keep = true
			c.close()
			foreign := stagePrefix + strings.Repeat("A", 26)
			if err := os.Mkdir(filepath.Join(dir, foreign), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, foreign, "user"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Recover(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, c.stage)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("owned orphan remains")
			}
			assertFile(t, filepath.Join(dir, foreign, "user"), []byte("keep"))
			if committed {
				assertFile(t, filepath.Join(dir, "a"), []byte("new"))
			}
			first, err := ReadLease(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first() }()
			second, err := ReadLease(dir)
			if err != nil {
				t.Fatalf("unowned directory blocked second reader: %v", err)
			}
			_ = second()
		})
	}
}

func TestHealthyUnpackDoesNotRequireWritableInstallerLock(t *testing.T) {
	dir := t.TempDir()
	archive := buildArchive(t, t.TempDir(), map[string][]byte{"a": []byte("valid")}, "")
	if err := Unpack(context.Background(), archive, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".catalog.lock")); err != nil {
		t.Fatal(err)
	}
	// A directory at the lock path reliably rejects lock creation on every OS.
	if err := os.Mkdir(filepath.Join(dir, ".catalog.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(context.Background(), "not-needed", dir, nil); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsUnsafeFilesBeforeWriting(t *testing.T) {
	for _, name := range []string{"../sibling.txt", `..\sibling.txt`, `/absolute`, `C:\target`, `file:stream`, "CON.txt", "com1", "LPT9.bin", "file.", "file ", ".catalog.lock", "a.part", manifestEntryName} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "sibling.txt"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			m := &Manifest{Files: []File{{Name: name, Size: 3, SHA256: sha256hex([]byte("bad"))}}}
			dest := filepath.Join(root, "catalog")
			if err := Fetch(context.Background(), dest, m, nil); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
			assertFile(t, filepath.Join(root, "sibling.txt"), []byte("keep"))
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid manifest created destination")
			}
		})
	}
	valid := File{Name: "catalog.sqlite", Size: 1, SHA256: sha256hex([]byte("a"))}
	for _, files := range [][]File{
		{valid, {Name: "CATALOG.SQLITE", Size: 1, SHA256: valid.SHA256}},
		{{Name: "a", Size: -1, SHA256: valid.SHA256}},
		{{Name: "a", Size: maxCatalogBytes + 1, SHA256: valid.SHA256}},
		{{Name: "a", Size: 1, SHA256: "bad"}},
		{{Name: "a", Size: maxCatalogBytes, SHA256: valid.SHA256}, valid},
	} {
		if err := (&Manifest{Files: files}).Validate(); err == nil {
			t.Fatalf("invalid files accepted: %+v", files)
		}
	}
}

func TestFetchDoesNotPublishAnyFilesWhenDownloadFails(t *testing.T) {
	srv, _, _ := newServer(t, false)
	m := testManifest(t, srv, "catalog.sqlite")
	dir := t.TempDir()
	for _, f := range m.Files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Fetch(context.Background(), dir, m, nil); err == nil {
		t.Fatal("expected checksum failure")
	}
	for _, f := range m.Files {
		assertFile(t, filepath.Join(dir, f.Name), []byte("old"))
	}
	assertNoStages(t, dir)
}

func TestFetchRepairsManifestWithoutDownloadingValidFiles(t *testing.T) {
	srv, reqA, reqB := newServer(t, false)
	m := testManifest(t, srv, "")
	dir := t.TempDir()
	for _, file := range []struct {
		name string
		data []byte
	}{{"vectors.i8", blobA}, {"catalog.sqlite", blobB}} {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Fetch(context.Background(), dir, m, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(context.Background(), filepath.Join(dir, manifestEntryName)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(reqA) != 0 || atomic.LoadInt32(reqB) != 0 {
		t.Fatal("manifest repair redownloaded verified catalog")
	}
}

func TestFetchRejectsOutsidePartialSymlink(t *testing.T) {
	srv, _, _ := newServer(t, false)
	m := testManifest(t, srv, "")
	root, dir := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(dir, m.Files[0].Name+partSuffix)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Fetch(context.Background(), dir, m, nil); err == nil {
		t.Fatal("outside partial symlink accepted")
	}
	assertFile(t, sentinel, []byte("keep"))
}

func TestCatalogPromotionRollbackPreservesOldGeneration(t *testing.T) {
	dir := t.TempDir()
	c, err := beginCatalogInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err := c.prepare(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := c.root.WriteFile(name, []byte("old-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSynced(c.root, filepath.Join(c.stage, "a"), []byte("new-a")); err != nil {
		t.Fatal(err)
	}
	// Simulate loss of the second prepared file at activation time. The first
	// rename succeeds, the second fails, and both originals must be restored.
	if err := c.publish([]string{"a", "b"}); err == nil {
		t.Fatal("expected second promotion failure")
	}
	for _, name := range []string{"a", "b"} {
		assertFile(t, filepath.Join(dir, name), []byte("old-"+name))
	}
	if _, err := c.root.Stat(transactionName); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful rollback left journal")
	}
}

func TestRecoverInterruptedCatalogPromotion(t *testing.T) {
	dir := t.TempDir()
	c, err := beginCatalogInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := c.root.Mkdir(filepath.Join(c.stage, ".previous"), 0o700); err != nil {
		t.Fatal(err)
	}
	j := catalogTransaction{Stage: c.stage, Files: []catalogReplacement{{Name: "a", Previous: true}, {Name: "b"}}}
	raw, _ := json.Marshal(j)
	if err := writeSynced(c.root, transactionName, raw); err != nil {
		t.Fatal(err)
	}
	if err := writeSynced(c.root, filepath.Join(c.stage, ".previous", "a"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := writeSynced(c.root, name, []byte("new")); err != nil {
			t.Fatal(err)
		}
	}
	c.keep = true
	c.close() // leave exactly the state a terminated activation would leave
	if err := Recover(dir); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "a"), []byte("old"))
	if _, err := os.Stat(filepath.Join(dir, "b")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new file survived rollback")
	}
	assertNoStages(t, dir)
	if err := Recover(dir); err != nil {
		t.Fatalf("recovery not idempotent: %v", err)
	}
}

func TestRecoverRetriesAfterPartiallyCompletedRollback(t *testing.T) {
	dir := t.TempDir()
	c, err := beginCatalogInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := c.root.Mkdir(filepath.Join(c.stage, ".previous"), 0o700); err != nil {
		t.Fatal(err)
	}
	j := catalogTransaction{Stage: c.stage, Files: []catalogReplacement{{Name: "a", Previous: true}, {Name: "b", Previous: true}}}
	raw, _ := json.Marshal(j)
	if err := writeSynced(c.root, transactionName, raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := writeSynced(c.root, filepath.Join(c.stage, ".previous", name), []byte("old-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSynced(c.root, "a", []byte("new-a")); err != nil {
		t.Fatal(err)
	}
	// A non-file blocks recovery after the first original has been restored.
	if err := c.root.Mkdir("b", 0o700); err != nil {
		t.Fatal(err)
	}
	c.keep = true
	c.close()
	if err := Recover(dir); err == nil {
		t.Fatal("expected blocked rollback")
	}
	assertFile(t, filepath.Join(dir, "a"), []byte("old-a"))
	if err := os.Remove(filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if err := Recover(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		assertFile(t, filepath.Join(dir, name), []byte("old-"+name))
	}
	assertNoStages(t, dir)
}

func TestUnpackBoundsAndTruncationPreserveCatalog(t *testing.T) {
	for _, scenario := range []string{"truncated", "oversized-header", "oversized-manifest", "duplicate", "traversal", "link"} {
		t.Run(scenario, func(t *testing.T) {
			var tarBytes bytes.Buffer
			tw := tar.NewWriter(&tarBytes)
			data := []byte("new catalog")
			m := Manifest{Files: []File{{Name: "catalog.sqlite", Size: int64(len(data)), SHA256: sha256hex(data)}}}
			raw, _ := json.Marshal(m)
			manifestSize := int64(len(raw))
			if scenario == "oversized-manifest" {
				manifestSize = maxManifestBytes + 1
			}
			if err := tw.WriteHeader(&tar.Header{Name: manifestEntryName, Mode: 0o600, Size: manifestSize}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(raw); err != nil {
				t.Fatal(err)
			}
			if scenario != "oversized-manifest" {
				name, size, kind := "catalog.sqlite", int64(len(data)), byte(tar.TypeReg)
				if scenario == "traversal" {
					name = "../catalog.sqlite"
				}
				if scenario == "oversized-header" {
					size++
				}
				if scenario == "link" {
					kind, size = tar.TypeSymlink, 0
				}
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, Typeflag: kind, Linkname: "outside"}); err != nil {
					t.Fatal(err)
				}
				if scenario == "truncated" {
					data = data[:2]
				}
				if kind == tar.TypeReg {
					if _, err := tw.Write(data); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "duplicate" {
					if err := tw.WriteHeader(&tar.Header{Name: name, Size: size, Mode: 0o600}); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Intentionally omit tar close/padding so truncated cases remain short,
			// but the zstd frame itself is complete and valid.
			zw, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}
			compressed := zw.EncodeAll(tarBytes.Bytes(), nil)
			zw.Close()
			archive := filepath.Join(t.TempDir(), "catalog.tar.zst")
			if err := os.WriteFile(archive, compressed, 0o600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "catalog.sqlite"), []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Unpack(context.Background(), archive, dir, nil); err == nil {
				t.Fatal("invalid archive accepted")
			}
			assertFile(t, filepath.Join(dir, "catalog.sqlite"), []byte("old"))
			assertNoStages(t, dir)
		})
	}
}

func assertNoStages(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagePrefix) || entry.Name() == transactionName {
			t.Fatalf("left transaction state: %s", entry.Name())
		}
	}
}
