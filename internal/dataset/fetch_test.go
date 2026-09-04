package dataset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/fakes"
)

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// blobs to serve
var (
	blobA = bytes.Repeat([]byte("playlist-ai-catalog-vectors-"), 4096) // ~112 KiB
	blobB = bytes.Repeat([]byte{0x1, 0x2, 0x3, 0x4, 0x5}, 20000)       // 100 KiB
)

// server serves blobA/blobB by name with full Range support and counts requests.
func newServer(t *testing.T, ignoreRange bool) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var reqA, reqB int32
	mux := http.NewServeMux()
	serve := func(name string, blob []byte, counter *int32) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(counter, 1)
			if ignoreRange {
				w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(blob)
				return
			}
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(blob))
		}
	}
	mux.HandleFunc("/vectors.i8", serve("vectors.i8", blobA, &reqA))
	mux.HandleFunc("/catalog.sqlite", serve("catalog.sqlite", blobB, &reqB))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &reqA, &reqB
}

func testManifest(t *testing.T, srv *httptest.Server, badHashFor string) *Manifest {
	t.Helper()
	mk := func(name string, blob []byte) File {
		h := sha256hex(blob)
		if name == badHashFor {
			h = sha256hex([]byte("not the real bytes"))
		}
		return File{Name: name, Size: int64(len(blob)), SHA256: h, URL: srv.URL + "/" + name}
	}
	m := &Manifest{
		Name:  "test",
		Files: []File{mk("vectors.i8", blobA), mk("catalog.sqlite", blobB)},
	}
	// round-trip through JSON so we exercise the real unmarshal path too
	raw, _ := json.Marshal(m)
	dir := t.TempDir()
	mpath := filepath.Join(dir, "catalog-manifest.json")
	if err := os.WriteFile(mpath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(context.Background(), mpath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestFetchFull(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, false)
	m := testManifest(t, srv, "")
	dir := t.TempDir()
	rec := &fakes.RecordingProgress{}

	if err := Fetch(context.Background(), dir, m, rec); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	assertFile(t, filepath.Join(dir, "vectors.i8"), blobA)
	assertFile(t, filepath.Join(dir, "catalog.sqlite"), blobB)
	assertNoPartials(t, dir)

	rows := rec.Snapshot()
	if len(rows) == 0 {
		t.Fatal("no progress reported")
	}
	var last int64
	total := m.TotalBytes()
	for _, r := range rows {
		if r.Op != ProgressOp {
			t.Fatalf("unexpected op %q", r.Op)
		}
		if r.Done < last {
			t.Fatalf("progress went backwards: %d then %d", last, r.Done)
		}
		last = r.Done
		if r.Total != total {
			t.Fatalf("total = %d, want %d", r.Total, total)
		}
	}
	if last != total {
		t.Fatalf("final progress %d, want %d", last, total)
	}
}

func TestFetchResumesFromPartial(t *testing.T) {
	t.Parallel()
	srv, reqA, _ := newServer(t, false)
	m := testManifest(t, srv, "")
	dir := t.TempDir()

	// Pre-seed a half-written .part with a correct prefix.
	half := len(blobA) / 2
	if err := os.WriteFile(filepath.Join(dir, "vectors.i8"+partSuffix), blobA[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Fetch(context.Background(), dir, m, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	assertFile(t, filepath.Join(dir, "vectors.i8"), blobA)
	if n := atomic.LoadInt32(reqA); n != 1 {
		t.Fatalf("vectors.i8 requested %d times, want 1", n)
	}
}

func TestFetchRejectsBadChecksum(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, false)
	m := testManifest(t, srv, "catalog.sqlite")
	dir := t.TempDir()

	err := Fetch(context.Background(), dir, m, nil)
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "catalog.sqlite")); !os.IsNotExist(statErr) {
		t.Fatal("bad file was promoted despite checksum failure")
	}
	assertNoPartials(t, dir)
}

func TestFetchHandlesServerIgnoringRange(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, true) // always 200 + full body
	m := testManifest(t, srv, "")
	dir := t.TempDir()

	// A stale .part that a range request would try to resume from.
	if err := os.WriteFile(filepath.Join(dir, "vectors.i8"+partSuffix), blobA[:1000], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Fetch(context.Background(), dir, m, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	assertFile(t, filepath.Join(dir, "vectors.i8"), blobA)
}

func TestStatus(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, false)
	m := testManifest(t, srv, "")
	dir := t.TempDir()

	if ok, missing := Status(dir, m); ok || len(missing) != 2 {
		t.Fatalf("Status before fetch: ok=%v missing=%v", ok, missing)
	}
	if err := Fetch(context.Background(), dir, m, nil); err != nil {
		t.Fatal(err)
	}
	if ok, missing := Status(dir, m); !ok || len(missing) != 0 {
		t.Fatalf("Status after fetch: ok=%v missing=%v", ok, missing)
	}
}

func TestFetchSkipsCompleteFiles(t *testing.T) {
	t.Parallel()
	srv, reqA, reqB := newServer(t, false)
	m := testManifest(t, srv, "")
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "vectors.i8"), blobA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(context.Background(), dir, m, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(reqA) != 0 {
		t.Fatal("re-downloaded an already-complete file")
	}
	if atomic.LoadInt32(reqB) != 1 {
		t.Fatal("missing file not downloaded")
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: %d bytes, want %d", path, len(got), len(want))
	}
}

func assertNoPartials(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == partSuffix {
			t.Fatalf("leftover partial: %s", e.Name())
		}
	}
}
