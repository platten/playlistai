package modelmgr

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		// Every curated model must carry a pinned size + sha256 so Download
		// verifies it — no silent, unverified downloads of a multi-GB model.
		if !m.Verified() || m.Size == 0 || len(m.SHA256) != 64 {
			t.Fatalf("model %q is not integrity-pinned: size=%d sha256=%q", m.ID, m.Size, m.SHA256)
		}
	}
	if !seenRecommended {
		t.Fatal("no recommended model")
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
	wantPriority := []string{
		"qwen3.5-35b-a3b-q4km",
		"qwen3.5-9b-q4km",
		"mistral-small-3.1-24b-instruct-q4km",
		"gemma-3-12b-it-qat-q4km",
		"qwen3.5-4b-q4km",
	}
	for i, id := range wantPriority {
		if i >= len(cat) {
			t.Fatalf("catalog ended before recommended priority[%d] = %q", i, id)
		}
		if cat[i].ID != id || !cat[i].Recommended {
			t.Fatalf("recommended priority[%d] = %+v, want %q", i, cat[i], id)
		}
	}
	if llamaModel, ok := Get("llama-3.2-3b-instruct-q4km"); !ok || llamaModel.Recommended {
		t.Fatalf("legacy Llama model must remain available but non-recommended: %+v, %v", llamaModel, ok)
	}
}

func TestRecommendationsFitGPUWithHeadroom(t *testing.T) {
	t.Parallel()
	models := []Model{
		{ID: "priority-large", SizeApprox: 8 << 30, Recommended: true},
		{ID: "priority-medium", SizeApprox: 4 << 30, Recommended: true},
		{ID: "priority-small", SizeApprox: 2 << 30, Recommended: true},
		{ID: "legacy", SizeApprox: 1 << 30, Recommended: false},
	}

	got := Recommendations(models, Hardware{
		GPUAvailable:       true,
		AvailableVRAMBytes: 6 << 30,
		ReserveBytes:       1 << 30,
	})
	if len(got) != 2 || got[0].ID != "priority-medium" || got[1].ID != "priority-small" {
		t.Fatalf("GPU recommendations = %+v", got)
	}
}

func TestRecommendationsCPUUsesTwoSmallestInPriorityOrder(t *testing.T) {
	t.Parallel()
	models := []Model{
		{ID: "large", SizeApprox: 8 << 30, Recommended: true},
		{ID: "small-second", SizeApprox: 3 << 30, Recommended: true},
		{ID: "medium", SizeApprox: 5 << 30, Recommended: true},
		{ID: "small-first", SizeApprox: 2 << 30, Recommended: true},
		{ID: "legacy-tiny", SizeApprox: 1 << 30, Recommended: false},
	}

	got := Recommendations(models, Hardware{})
	if len(got) != 2 || got[0].ID != "small-second" || got[1].ID != "small-first" {
		t.Fatalf("CPU recommendations = %+v", got)
	}
}

func TestCatalogRecommendationsForCommonHardware(t *testing.T) {
	t.Parallel()
	ids := func(models []Model) []string {
		out := make([]string, len(models))
		for i := range models {
			out[i] = models[i].ID
		}
		return out
	}
	assertIDs := func(name string, got []Model, want ...string) {
		t.Helper()
		gotIDs := ids(got)
		if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
			t.Fatalf("%s recommendations = %v, want %v", name, gotIDs, want)
		}
	}

	all := []string{
		"qwen3.5-35b-a3b-q4km",
		"qwen3.5-9b-q4km",
		"mistral-small-3.1-24b-instruct-q4km",
		"gemma-3-12b-it-qat-q4km",
		"qwen3.5-4b-q4km",
	}
	reserve := int64(1 << 30)
	tests := []struct {
		name      string
		hardware  Hardware
		wantModel []string
	}{
		{name: "CPU", wantModel: []string{"qwen3.5-9b-q4km", "qwen3.5-4b-q4km"}},
		{name: "4 GiB GPU", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 4 << 30, ReserveBytes: reserve}, wantModel: []string{"qwen3.5-4b-q4km"}},
		{name: "test RTX 5060 observed free VRAM", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 7033 << 20, ReserveBytes: reserve}, wantModel: []string{"qwen3.5-9b-q4km", "qwen3.5-4b-q4km"}},
		{name: "8 GiB GPU", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 8 << 30, ReserveBytes: reserve}, wantModel: []string{"qwen3.5-9b-q4km", "gemma-3-12b-it-qat-q4km", "qwen3.5-4b-q4km"}},
		{name: "16 GiB GPU", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 16 << 30, ReserveBytes: reserve}, wantModel: []string{"qwen3.5-9b-q4km", "mistral-small-3.1-24b-instruct-q4km", "gemma-3-12b-it-qat-q4km", "qwen3.5-4b-q4km"}},
		// NVIDIA advertises 24 GB for the RTX 5090 Laptop GPU and 32 GB for
		// the desktop RTX 5090. llama.cpp reports capacity in MiB, so these
		// profiles use the corresponding 24/32 GiB binary-memory values.
		{name: "RTX 5090 Laptop 24 GiB", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 24 << 30, ReserveBytes: reserve}, wantModel: all},
		{name: "RTX 5090 desktop 32 GiB", hardware: Hardware{GPUAvailable: true, AvailableVRAMBytes: 32 << 30, ReserveBytes: reserve}, wantModel: all},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertIDs(tc.name, Recommendations(Catalog(), tc.hardware), tc.wantModel...)
		})
	}
}

func TestRecommendationsHonorEveryModelFitBoundary(t *testing.T) {
	t.Parallel()
	const reserve = int64(1 << 30)
	for _, model := range Catalog() {
		if !model.Recommended {
			continue
		}
		t.Run(model.ID, func(t *testing.T) {
			fits := func(available int64) bool {
				for _, got := range Recommendations(Catalog(), Hardware{
					GPUAvailable: true, AvailableVRAMBytes: available, ReserveBytes: reserve,
				}) {
					if got.ID == model.ID {
						return true
					}
				}
				return false
			}
			threshold := model.SizeApprox + reserve
			if !fits(threshold) {
				t.Fatalf("model should fit at exact weight + reserve threshold %d", threshold)
			}
			if fits(threshold - 1) {
				t.Fatalf("model should not fit one byte below threshold %d", threshold)
			}
		})
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

func TestDownloadSkipsAlreadyInstalled(t *testing.T) {
	t.Parallel()
	blob := fakeGGUF(50000)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.ServeContent(w, r, "m.gguf", time.Time{}, bytes.NewReader(blob))
	}))
	defer srv.Close()

	m := Model{ID: "test-model", Label: "Test", URL: srv.URL + "/m.gguf", Size: int64(len(blob))}
	dir := t.TempDir()

	if _, err := Download(context.Background(), m, dir, nil); err != nil {
		t.Fatalf("first Download: %v", err)
	}
	if hits != 1 {
		t.Fatalf("first download made %d requests, want 1", hits)
	}
	// Second call: the file is already there and valid — no network.
	rec := &fakes.RecordingProgress{}
	path, err := Download(context.Background(), m, dir, rec)
	if err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if hits != 1 {
		t.Fatalf("second Download re-fetched (%d requests total)", hits)
	}
	if path != filepath.Join(dir, "test-model.gguf") {
		t.Fatalf("path = %s", path)
	}
	if rows := rec.Snapshot(); len(rows) == 0 || rows[len(rows)-1].Note != "ready" {
		t.Fatalf("expected a terminal 'ready' progress report, got %+v", rows)
	}

	// A size mismatch on disk => not "installed" => re-download.
	if err := os.WriteFile(path, fakeGGUF(10), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsInstalled(m, dir) {
		t.Fatal("truncated file reported as installed")
	}
	if _, err := Download(context.Background(), m, dir, nil); err != nil {
		t.Fatalf("re-Download after truncation: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected a re-fetch after truncation, hits=%d", hits)
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
