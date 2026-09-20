package discoveryasset

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/modelpack"
)

type transportFixture struct {
	dir, pack string
	manifest  modelpack.Manifest
}
type progressCallback func(string, int64, int64, string)

func (f progressCallback) Report(op string, done, total int64, note string) { f(op, done, total, note) }

func TestLocalImportRejectsSourceChangedAfterInitialHash(t *testing.T) {
	ctx := context.Background()
	first, second := makeTransport(t, "original"), makeTransport(t, "changed")
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	installed, e := m.ImportLocal(ctx, first.pack, nil)
	if e != nil {
		t.Fatal(e)
	}
	replacement, e := os.ReadFile(second.pack)
	if e != nil {
		t.Fatal(e)
	}
	changed := false
	p := progressCallback(func(_ string, _, _ int64, _ string) {
		if !changed {
			changed = true
			if err := os.WriteFile(first.pack, replacement, 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if _, e = m.ImportLocal(ctx, first.pack, p); e == nil || !strings.Contains(e.Error(), "changed while importing") {
		t.Fatalf("source mutation not rejected: %v", e)
	}
	if m.Status().ManifestDigest != installed.ManifestDigest {
		t.Fatal("source mutation replaced active override")
	}
}

func TestAbsolutePartExportIncludesOfflineManifest(t *testing.T) {
	ctx := context.Background()
	fixture := makeTransport(t, "absolute")
	handler, server := tlsTransportServer(t, fixture)
	handler.mu.Lock()
	handler.fixture.manifest.Parts = append([]modelpack.Part(nil), fixture.manifest.Parts...)
	for i := range handler.fixture.manifest.Parts {
		handler.fixture.manifest.Parts[i].Path = server.URL + "/" + handler.fixture.manifest.Parts[i].Path
	}
	handler.mu.Unlock()
	destination := filepath.Join(t.TempDir(), "saved")
	result, e := DownloadArchive(ctx, server.URL+"/manifest.json", destination, nil)
	if e != nil {
		t.Fatal(e)
	}
	if result.Files != len(fixture.manifest.Parts)+2 {
		t.Fatal("offline companion not reported")
	}
	server.Close()
	if e = modelpack.Fetch(ctx, filepath.Join(destination, "manifest.offline.json"), filepath.Join(t.TempDir(), "cache"), filepath.Join(t.TempDir(), "unpacked"), nil); e != nil {
		t.Fatalf("absolute-parts export cannot be used offline: %v", e)
	}
}

func TestAbsolutePartExportRejectsNonPortableBasenames(t *testing.T) {
	fixture := makeTransport(t, "portable")
	handler, server := tlsTransportServer(t, fixture)
	for _, name := range []string{"NUL", "CON.bin", "chunk."} {
		t.Run(name, func(t *testing.T) {
			handler.mu.Lock()
			handler.fixture.manifest.Parts = append([]modelpack.Part(nil), fixture.manifest.Parts...)
			handler.fixture.manifest.Parts[0].Path = server.URL + "/" + name
			handler.mu.Unlock()
			destination := filepath.Join(t.TempDir(), "saved")
			if _, e := DownloadArchive(context.Background(), server.URL+"/manifest.json", destination, nil); e == nil {
				t.Fatal("non-portable absolute basename accepted")
			}
			if _, e := os.Stat(destination); !os.IsNotExist(e) {
				t.Fatal("failed export published destination")
			}
		})
	}
}

func makeTransport(t *testing.T, label string) transportFixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	pack := filepath.Join(source, "output.paipack")
	_, e := librarypack.Write(context.Background(), pack, librarypack.Pack{CorpusGeneration: label, MetadataGeneration: label, Tracks: []librarypack.Track{{ID: "original-id", Artist: "Artist", Title: label, Album: "Album", RawTags: []byte(`{"genre":"ambient","mood":"calm","originaldate":"1995"}`)}}}, librarypack.Limits{})
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "transport")
	m, e := modelpack.Package(context.Background(), "same-name", source, dir, 1024)
	if e != nil {
		t.Fatal(e)
	}
	return transportFixture{dir, pack, m}
}

type transportServer struct {
	mu       sync.Mutex
	fixture  transportFixture
	requests int
	noCache  bool
}

func (s *transportServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/manifest.json" {
		s.requests++
		s.noCache = r.Header.Get("Cache-Control") == "no-cache"
		_ = json.NewEncoder(w).Encode(s.fixture.manifest)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.fixture.dir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))))
}
func tlsTransportServer(t *testing.T, fixture transportFixture) (*transportServer, *httptest.Server) {
	t.Helper()
	handler := &transportServer{fixture: fixture}
	server := httptest.NewTLSServer(handler)
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { server.Close(); http.DefaultTransport = previous })
	return handler, server
}
func TestMultipartFreshSnapshotUpdateOverrideAndRollback(t *testing.T) {
	ctx := context.Background()
	first := makeTransport(t, "first")
	second := makeTransport(t, "second")
	handler, server := tlsTransportServer(t, first)
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	status, e := m.Install(ctx, server.URL+"/manifest.json", nil)
	if e != nil {
		t.Fatal(e)
	}
	if !status.Installed || status.Source != "hosted" || status.ManifestDigest == "" {
		t.Fatalf("bad status: %+v", status)
	}
	handler.mu.Lock()
	requests := handler.requests
	noCache := handler.noCache
	handler.mu.Unlock()
	if requests != 1 || !noCache {
		t.Fatalf("manifest snapshot refetched=%d noCache=%v", requests, noCache)
	}
	pack, e := inspectPackManifest(ctx, first.pack)
	if e != nil {
		t.Fatal(e)
	}
	if status.PackIDs[0] != pack.PackID {
		t.Fatal("hosted pack was rewritten")
	}
	update, e := m.CheckUpdate(ctx, server.URL+"/manifest.json")
	if e != nil || update.UpdateAvailable {
		t.Fatalf("unchanged update %+v %v", update, e)
	}
	handler.mu.Lock()
	handler.fixture = second
	handler.mu.Unlock()
	update, e = m.CheckUpdate(ctx, server.URL+"/manifest.json")
	if e != nil || !update.UpdateAvailable || update.Digest == status.ManifestDigest {
		t.Fatalf("same-name changed content missed %+v %v", update, e)
	}
	local, e := m.ImportLocal(ctx, first.pack, nil)
	if e != nil || local.Source != "local" {
		t.Fatalf("local %+v %v", local, e)
	}
	root := m.root
	_ = m.Close()
	m, e = Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if m.Status().Source != "local" {
		t.Fatal("local override lost on restart")
	}
	handler.mu.Lock()
	handler.fixture.manifest.Parts = append([]modelpack.Part(nil), second.manifest.Parts...)
	handler.fixture.manifest.Parts[0].SHA256 = strings.Repeat("c", 64)
	handler.mu.Unlock()
	if _, e = m.Install(ctx, server.URL+"/manifest.json", nil); e == nil {
		t.Fatal("bad transport hash accepted")
	}
	if m.Status().Source != "local" || m.Status().ManifestDigest != local.ManifestDigest {
		t.Fatal("failed hosted update replaced local override")
	}
	handler.mu.Lock()
	handler.fixture = second
	handler.mu.Unlock()
	restored, e := m.Install(ctx, server.URL+"/manifest.json", nil)
	if e != nil || restored.Source != "hosted" || restored.ManifestDigest == status.ManifestDigest {
		t.Fatalf("restore %+v %v", restored, e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = m.ImportLocal(canceled, first.pack, nil); e == nil {
		t.Fatal("canceled import accepted")
	}
	if m.Status().ManifestDigest != restored.ManifestDigest {
		t.Fatal("cancellation changed active data")
	}
}
func TestArchivePreservesSnapshotAndScratchCollisionNames(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "scratch-first", true: "target-first"}[reverse], func(t *testing.T) {
			ctx := context.Background()
			fixture := makeTransport(t, "export")
			if len(fixture.manifest.Parts) < 2 {
				t.Fatal("fixture must have multiple parts")
			}
			names := []string{"chunk.part", "chunk"}
			if reverse {
				names = []string{"chunk", "chunk.part"}
			}
			for i := 0; i < 2; i++ {
				old := fixture.manifest.Parts[i].Path
				if e := os.Rename(filepath.Join(fixture.dir, old), filepath.Join(fixture.dir, names[i])); e != nil {
					t.Fatal(e)
				}
				fixture.manifest.Parts[i].Path = names[i]
			}
			_, server := tlsTransportServer(t, fixture)
			destination := filepath.Join(t.TempDir(), "saved")
			result, e := DownloadArchive(ctx, server.URL+"/manifest.json", destination, nil)
			if e != nil {
				t.Fatal(e)
			}
			if result.Files != len(fixture.manifest.Parts)+1 {
				t.Fatal(result)
			}
			raw, e := os.ReadFile(filepath.Join(destination, "manifest.json"))
			if e != nil {
				t.Fatal(e)
			}
			want, _ := json.Marshal(fixture.manifest)
			if !bytes.Equal(bytes.TrimSpace(raw), want) {
				t.Fatal("saved manifest changed")
			}
			if e = modelpack.Fetch(ctx, filepath.Join(destination, "manifest.json"), filepath.Join(t.TempDir(), "cache"), filepath.Join(t.TempDir(), "unpacked"), nil); e != nil {
				t.Fatalf("saved archive not complete/offline usable: %v", e)
			}
			if _, e = DownloadArchive(ctx, server.URL+"/manifest.json", destination, nil); e == nil {
				t.Fatal("existing destination overwritten")
			}
		})
	}
}
func TestMultipartRejectsWrongFilesAndOversizedDownload(t *testing.T) {
	fixture := makeTransport(t, "bounds")
	bad := fixture.manifest
	bad.Files = append([]modelpack.File(nil), bad.Files...)
	bad.Files[0].Path = "model.onnx"
	if validateMultipart(bad, 100) == nil {
		t.Fatal("non-paipack accepted")
	}
	bad = fixture.manifest
	bad.Parts = append([]modelpack.Part(nil), bad.Parts...)
	for len(bad.Parts) < 20 {
		part := bad.Parts[0]
		part.Path = strings.Repeat("x", len(bad.Parts)) + ".part"
		part.Size = 190000000
		bad.Parts = append(bad.Parts, part)
	}
	if validateMultipart(bad, 100) == nil {
		t.Fatal("oversized transport accepted")
	}
}

func TestHostedMultipartOptIn(t *testing.T) {
	location := os.Getenv("PLAYLISTAI_DISCOVERY_MANIFEST_URL")
	if location == "" {
		t.Skip("set PLAYLISTAI_DISCOVERY_MANIFEST_URL to opt into real HTTPS multipart install")
	}
	ctx := context.Background()
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	started := time.Now()
	status, e := m.Install(ctx, location, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !status.Installed || status.Source != "hosted" || status.Tracks == 0 {
		t.Fatalf("bad hosted status %+v", status)
	}
	catalogs, release, e := m.Pin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if len(catalogs) != len(status.PackIDs) {
		t.Fatal("incomplete release pins")
	}
	t.Logf("fresh HTTPS install: %d transport bytes, %d packs, %d tracks, %s elapsed", status.DownloadBytes, len(catalogs), status.Tracks, time.Since(started))
}
