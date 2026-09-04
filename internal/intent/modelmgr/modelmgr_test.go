package modelmgr

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/fakes"
)

func fakeGGUF(n int) []byte {
	b := make([]byte, n)
	copy(b, "GGUF")
	for i := 4; i < n; i++ {
		b[i] = byte(i % 251)
	}
	return b
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	cat := Catalog()
	if len(cat) < 2 {
		t.Fatalf("catalog has %d models", len(cat))
	}
	seenRecommended := false
	for _, m := range cat {
		if m.ID == "" || m.Label == "" || m.URL == "" {
			t.Fatalf("incomplete model: %+v", m)
		}
		if m.Recommended {
			seenRecommended = true
		}
		if got, ok := Get(m.ID); !ok || got.URL != m.URL {
			t.Fatalf("Get(%q) mismatch", m.ID)
		}
	}
	if !seenRecommended {
		t.Fatal("no recommended model")
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
}

func TestValidateGGUF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	good := filepath.Join(dir, "good.gguf")
	if err := os.WriteFile(good, fakeGGUF(64), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGGUF(good); err != nil {
		t.Fatalf("valid GGUF rejected: %v", err)
	}

	bad := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(bad, []byte("this is just text, not a model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGGUF(bad); err == nil {
		t.Fatal("text file accepted as GGUF")
	}

	if err := ValidateGGUF(filepath.Join(dir, "missing.gguf")); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := ValidateGGUF(dir); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()
	blob := fakeGGUF(50000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "m.gguf", time.Time{}, bytes.NewReader(blob))
	}))
	defer srv.Close()

	m := Model{ID: "test-model", Label: "Test", URL: srv.URL + "/m.gguf"}
	dir := t.TempDir()
	rec := &fakes.RecordingProgress{}

	path, err := Download(context.Background(), m, dir, rec)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if path != filepath.Join(dir, "test-model.gguf") {
		t.Fatalf("path = %s", path)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, blob) {
		t.Fatalf("content mismatch: %d vs %d bytes", len(got), len(blob))
	}
	if err := ValidateGGUF(path); err != nil {
		t.Fatalf("downloaded file failed validation: %v", err)
	}

	rows := rec.Snapshot()
	if len(rows) == 0 || rows[0].Op != ProgressOp {
		t.Fatalf("progress: %+v", rows)
	}
	if rows[len(rows)-1].Note != "ready" {
		t.Fatalf("last progress note = %q", rows[len(rows)-1].Note)
	}
}

func TestDownloadRejectsNonGGUF(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>404 not found</html>"))
	}))
	defer srv.Close()

	m := Model{ID: "x", Label: "X", URL: srv.URL + "/m.gguf"}
	dir := t.TempDir()
	if _, err := Download(context.Background(), m, dir, nil); err == nil {
		t.Fatal("expected an error for a non-GGUF response")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.gguf")); !os.IsNotExist(err) {
		t.Fatal("bad download was left on disk")
	}
}

func TestInstalled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.gguf"), fakeGGUF(16), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.GGUF"), fakeGGUF(16), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)

	got := Installed(dir)
	if len(got) != 2 {
		t.Fatalf("Installed = %v", got)
	}
}
