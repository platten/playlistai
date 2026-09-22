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
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/modelpack"
)

type transportFixture struct {
	dir, pack string
	manifest  modelpack.Manifest
}
type progressCallback func(string, int64, int64, string)

func TestManualSplitManifestExplainsHostedFormat(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":1,"format":"playlist-ai-paipack-parts","parts":[{"name":"library.paipack.part000"}]}`))
	}))
	defer server.Close()
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	_, err := fetchRemote(context.Background(), server.URL+"/manifest.json")
	if err == nil || !strings.Contains(err.Error(), "paipack-split --hosted") {
		t.Fatalf("manual split manifest did not explain the recovery: %v", err)
	}
}

func TestRemoteManifestCannotClaimGeneratedCompanion(t *testing.T) {
	_, manifest := fixtureRelease(t, "remote-generated-forgery")
	manifest.Packs[0].Size = 2_456_997_645
	manifest.Companion.Size = 600_000_000
	manifest.Source = "local"
	manifest.ManifestDigest = strings.Repeat("a", 64)
	manifest.TransportFormat = "paipack-v5"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	if _, err := fetchRemote(context.Background(), server.URL+"/manifest.json"); err == nil || !strings.Contains(err.Error(), "local activation fields") {
		t.Fatalf("remote manifest spoofed locally generated companion: %v", err)
	}
	if _, err := FetchManifest(context.Background(), server.URL+"/manifest.json"); err == nil || !strings.Contains(err.Error(), "local activation fields") {
		t.Fatalf("direct manifest fetch accepted local activation fields: %v", err)
	}
}

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
	if installed.DownloadBytes != 0 {
		t.Fatalf("local import reported a network download: %d bytes", installed.DownloadBytes)
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

func TestRealLocalPaipackImportOptIn(t *testing.T) {
	path := os.Getenv("PLAYLISTAI_DISCOVERY_PAIPACK")
	if path == "" {
		t.Skip("set PLAYLISTAI_DISCOVERY_PAIPACK to explicitly test a local paipack import")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	m, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	status, err := m.ImportLocal(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Source != "local" || status.Tracks == 0 {
		t.Fatalf("unexpected import status: %+v", status)
	}
	t.Logf("imported %d tracks; indexed pack bytes=%d", status.Tracks, m.active.manifest.Packs[0].Size)
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
	if _, e = BuildIndexedFromPack(context.Background(), pack, pack, librarypack.Limits{}); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "transport")
	m, e := modelpack.Package(context.Background(), "same-name", source, dir, 1024)
	if e != nil {
		t.Fatal(e)
	}
	return transportFixture{dir, pack, m}
}

func TestIndexedPackImportUsesEmbeddedIndexesAfterRestart(t *testing.T) {
	ctx := context.Background()
	fixture := makeTransport(t, "indexed-round-trip")
	pack, err := inspectPackManifest(ctx, fixture.pack)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Version != librarypack.IndexedFormatVersion || len(pack.IndexFiles) < 2 {
		t.Fatalf("indexer output lacks packaged indexes: %+v", pack)
	}
	root := t.TempDir()
	m, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.ImportLocal(ctx, fixture.pack, nil)
	if err != nil || !status.Installed || status.Tracks != 1 || !m.active.manifest.EmbeddedIndexes || m.active.manifest.TransportFormat != "paipack-v8" || m.active.manifest.Companion != (File{}) {
		t.Fatalf("indexed import: status=%+v err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Join(m.active.dir, "discovery.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("installer generated an external companion: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	catalogs, release, err := m.Pin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	hits, err := catalogs[0].Search(ctx, localcatalog.MetadataQuery{Text: "ambient", Limit: 5})
	if err != nil || len(hits) != 1 {
		t.Fatalf("packaged search index is unusable: hits=%d err=%v", len(hits), err)
	}
}

func TestIndexedImportBudgetsExtractedIndexBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.paipack")
	if _, err := librarypack.Write(ctx, source, librarypack.Pack{CorpusGeneration: "large-index", MetadataGeneration: "large-index", Tracks: []librarypack.Track{{ID: "one", Artist: "Artist", Title: "One"}}}, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	staging, err := librarypack.OpenManager(ctx, filepath.Join(root, "staging"), packLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer staging.Close()
	staged, err := staging.Stage(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staging.Discard(staged) }()
	if err := localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1}); err != nil {
		t.Fatal(err)
	}
	if err := BuildEmbeddedCompanion(ctx, staged.Generation()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged.Generation().Directory(), "local-index-v3", "padding.bin"), bytes.Repeat([]byte("x"), 5<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(root, "indexed.paipack")
	manifest, err := librarypack.WriteIndexed(ctx, pack, staged.Generation(), packLimits())
	if err != nil {
		t.Fatal(err)
	}
	var inner int64
	for _, file := range manifest.IndexFiles {
		inner += file.Size
	}
	if inner <= 4<<20 {
		t.Fatalf("fixture did not exceed the former index expansion allowance: %d", inner)
	}
	m, err := Open(ctx, filepath.Join(root, "installed"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.ImportLocal(ctx, pack, nil); err != nil {
		t.Fatalf("indexed pack rejected after accounting for its extracted index bytes: %v", err)
	}
	if m.active.manifest.Packs[0].ExpandedBytes != expandedPackBytes(manifest) {
		t.Fatal("release omitted extracted index bytes from its expansion budget")
	}
}

func TestLocalImportRejectsPackWithoutPrebuiltIndexes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.paipack")
	if _, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "old", MetadataGeneration: "old", Tracks: []librarypack.Track{{ID: "one", Artist: "Artist", Title: "One"}}}, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	m, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.ImportLocal(ctx, path, nil); err == nil || !strings.Contains(err.Error(), "re-export with playlist-indexer") {
		t.Fatalf("unindexed import was accepted: %v", err)
	}
	if m.Status().Installed {
		t.Fatal("unindexed pack replaced active discovery")
	}
}

func TestIndexedPackTamperedSearchIndexFailsRestart(t *testing.T) {
	ctx := context.Background()
	fixture := makeTransport(t, "indexed-tamper")
	root := t.TempDir()
	m, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ImportLocal(ctx, fixture.pack, nil); err != nil {
		t.Fatal(err)
	}
	lease, err := m.active.managers[0].Pin()
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(lease.Generation().Directory(), "local-index-v3", "metadata.sqlite")
	lease.Release()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(indexPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if status := m.Status(); status.Installed || !strings.Contains(status.Error, "checksum mismatch") {
		t.Fatalf("tampered packaged index was accepted: %+v", status)
	}
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
	total := int64(100)
	for _, part := range bad.Parts {
		total += part.Size
	}
	for total <= MaxIndexedDownloadBytes {
		part := bad.Parts[0]
		part.Path = strings.Repeat("x", len(bad.Parts)) + ".part"
		part.Size = 190000000
		bad.Parts = append(bad.Parts, part)
		total += part.Size
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
