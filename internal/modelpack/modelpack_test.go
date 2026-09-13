package modelpack

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/ports"
)

func checksum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fixture(t *testing.T, headers []*tar.Header, payloads []string) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	var compressed bytes.Buffer
	zw, e := zstd.NewWriter(&compressed)
	if e != nil {
		t.Fatal(e)
	}
	tw := tar.NewWriter(zw)
	m := Manifest{Version: 1, Name: "test"}
	for i, h := range headers {
		if e = tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			if _, e = tw.Write([]byte(payloads[i])); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e = zw.Close(); e != nil {
		t.Fatal(e)
	}
	b := compressed.Bytes()
	for i, start := 0, 0; start < len(b); i, start = i+1, start+17 {
		end := min(start+17, len(b))
		chunk := b[start:end]
		name := string(rune('a'+i)) + ".part"
		if e = os.WriteFile(filepath.Join(dir, name), chunk, 0o600); e != nil {
			t.Fatal(e)
		}
		m.Parts = append(m.Parts, Part{Path: name, Size: int64(len(chunk)), SHA256: checksum(chunk)})
	}
	m.Files = []File{{Path: "nested/model.onnx", Size: 5, SHA256: checksum([]byte("model"))}}
	location := filepath.Join(dir, "manifest.json")
	writeManifest(t, location, m)
	return location, m
}

func TestHTTPSResumeAndCancellation(t *testing.T) {
	location, m := validFixture(t)
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if e := os.MkdirAll(cache, 0o755); e != nil {
		t.Fatal(e)
	}
	first, e := os.ReadFile(filepath.Join(filepath.Dir(location), m.Parts[0].Path))
	if e != nil {
		t.Fatal(e)
	}
	partial := filepath.Join(cache, m.Parts[0].SHA256+".partdata.part")
	if e = os.WriteFile(partial, first[:5], 0o600); e != nil {
		t.Fatal(e)
	}
	var resumed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/") == m.Parts[0].Path && r.Header.Get("Range") == "bytes=5-" {
			resumed.Store(true)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 5-%d/%d", len(first)-1, len(first)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(first[5:])
			return
		}
		http.ServeFile(w, r, filepath.Join(filepath.Dir(location), filepath.FromSlash(r.URL.Path)))
	}))
	defer server.Close()
	previous := http.DefaultClient
	http.DefaultClient = server.Client()
	defer func() { http.DefaultClient = previous }()
	if e = Fetch(context.Background(), server.URL+"/manifest.json", cache, filepath.Join(root, "out"), nil); e != nil {
		t.Fatal(e)
	}
	if !resumed.Load() {
		t.Fatal("did not resume existing partial segment")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if e = Fetch(ctx, server.URL+"/manifest.json", cache, filepath.Join(root, "canceled"), ports.ProgressFunc(func(string, int64, int64, string) { cancel() })); !errors.Is(e, context.Canceled) {
		t.Fatalf("progress cancellation: %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "canceled")); !os.IsNotExist(e) {
		t.Fatal("canceled installation published")
	}
}
func writeManifest(t *testing.T, location string, m Manifest) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(location, b, 0o600); e != nil {
		t.Fatal(e)
	}
}
func validFixture(t *testing.T) (string, Manifest) {
	return fixture(t, []*tar.Header{{Name: "nested/model.onnx", Typeflag: tar.TypeReg, Size: 5, Mode: 0o644}}, []string{"model"})
}

func TestFetchSplitRoundtripAndCachedParts(t *testing.T) {
	location, m := validFixture(t)
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	dest := filepath.Join(root, "first")
	if e := Fetch(context.Background(), location, cache, dest, nil); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dest, "nested/model.onnx"))
	if e != nil || string(b) != "model" {
		t.Fatalf("payload %q, %v", b, e)
	}
	for _, p := range m.Parts {
		if e = os.Remove(filepath.Join(filepath.Dir(location), p.Path)); e != nil {
			t.Fatal(e)
		}
	}
	if e = Fetch(context.Background(), location, cache, filepath.Join(root, "second"), nil); e != nil {
		t.Fatalf("cached retry: %v", e)
	}
	if e = Fetch(context.Background(), location, cache, dest, nil); e == nil {
		t.Fatal("replaced existing destination")
	}
}

func TestFetchRejectsCorruptionAndCancellation(t *testing.T) {
	location, m := validFixture(t)
	root := t.TempDir()
	part := filepath.Join(filepath.Dir(location), m.Parts[0].Path)
	if e := os.WriteFile(part, bytes.Repeat([]byte{'x'}, int(m.Parts[0].Size)), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := Fetch(context.Background(), location, filepath.Join(root, "cache"), filepath.Join(root, "out"), nil); e == nil {
		t.Fatal("accepted corrupt part")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Fetch(ctx, location, filepath.Join(root, "cache"), filepath.Join(root, "out"), nil); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation: %v", e)
	}
}

func TestDecodedHashMismatchAndExtractionCancellation(t *testing.T) {
	location, m := validFixture(t)
	root := t.TempDir()
	m.Files[0].SHA256 = checksum([]byte("other"))
	writeManifest(t, location, m)
	if e := Fetch(context.Background(), location, filepath.Join(root, "cache"), filepath.Join(root, "out"), nil); e == nil {
		t.Fatal("accepted decoded checksum mismatch")
	}
	names := make([]string, 0, len(m.Parts))
	for _, p := range m.Parts {
		names = append(names, filepath.Join(root, "cache", p.SHA256+".partdata"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := extract(ctx, names, filepath.Join(root, "stage"), m.Files); !errors.Is(e, context.Canceled) {
		t.Fatalf("extraction cancellation: %v", e)
	}
}

func TestUnsafeOrIncompleteArchivesLeaveNoDestination(t *testing.T) {
	cases := map[string][]*tar.Header{
		"traversal": {{Name: "../escape", Typeflag: tar.TypeReg, Size: 5}},
		"symlink":   {{Name: "nested/model.onnx", Typeflag: tar.TypeSymlink, Linkname: "../escape"}},
		"duplicate": {{Name: "nested/model.onnx", Typeflag: tar.TypeReg, Size: 5}, {Name: "nested/model.onnx", Typeflag: tar.TypeReg, Size: 5}},
		"missing":   {},
		"size":      {{Name: "nested/model.onnx", Typeflag: tar.TypeReg, Size: 4}},
		"unlisted":  {{Name: "surprise", Typeflag: tar.TypeReg, Size: 5}},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			payloads := make([]string, len(headers))
			for i, h := range headers {
				payloads[i] = "model"[:int(h.Size)]
			}
			location, _ := fixture(t, headers, payloads)
			root := t.TempDir()
			dest := filepath.Join(root, "out")
			if e := Fetch(context.Background(), location, filepath.Join(root, "cache"), dest, nil); e == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, e := os.Stat(dest); !os.IsNotExist(e) {
				t.Fatalf("destination exists: %v", e)
			}
			stages, _ := filepath.Glob(filepath.Join(root, ".modelpack-*"))
			if len(stages) != 0 {
				t.Fatal("left staging files")
			}
		})
	}
}

func TestManifestValidation(t *testing.T) {
	_, good := validFixture(t)
	for _, name := range []string{"../bad", "a\\b", "/absolute", "C:/bad", "a/../b", "CON", "folder/AUX.txt", "name.", "nested/model.onnx:stream"} {
		t.Run(name, func(t *testing.T) {
			m := good
			m.Files = []File{{Path: name, Size: 1, SHA256: checksum(nil)}}
			if m.Validate() == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
	m := good
	m.Parts = append([]Part(nil), good.Parts...)
	m.Parts[0].Size = MaxPartBytes
	if m.Validate() == nil {
		t.Fatal("accepted upload limit")
	}
	m = good
	m.Files = []File{{Path: "a", SHA256: checksum(nil)}, {Path: "a/b", SHA256: checksum(nil)}}
	if m.Validate() == nil {
		t.Fatal("accepted file-directory conflict")
	}
	m = good
	m.Files = nil
	for i := 0; i < 5; i++ {
		m.Files = append(m.Files, File{Path: fmt.Sprintf("file%d", i), Size: maxFileBytes, SHA256: checksum(nil)})
	}
	if m.Validate() == nil {
		t.Fatal("accepted cumulative extracted size limit")
	}
}

func TestRejectHTTPSDowngradeForManifestAndPart(t *testing.T) {
	location, m := validFixture(t)
	var insecureRequests atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureRequests.Add(1); http.ServeFile(w, r, location) }))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, insecure.URL, http.StatusFound) }))
	defer secure.Close()
	previous := http.DefaultClient
	http.DefaultClient = secure.Client()
	defer func() { http.DefaultClient = previous }()
	if _, e := ReadManifest(context.Background(), secure.URL); e == nil {
		t.Fatal("accepted manifest downgrade")
	}
	m.Parts[0].Path = secure.URL + "/part"
	writeManifest(t, location, m)
	root := t.TempDir()
	if e := Fetch(context.Background(), location, filepath.Join(root, "cache"), filepath.Join(root, "out"), nil); e == nil {
		t.Fatal("accepted segment downgrade")
	}
	if insecureRequests.Load() != 0 {
		t.Fatal("contacted insecure redirected host")
	}
}
