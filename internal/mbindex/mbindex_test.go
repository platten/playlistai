package mbindex

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/platten/playlistai/internal/ports"
)

func TestBuildPackageVerifyAndInstall(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	artistArchive := filepath.Join(root, "artist.tar.xz")
	recordingArchive := filepath.Join(root, "recording.tar.xz")
	writeDump(t, artistArchive, "artist", []any{
		map[string]any{"id": "artist-1", "name": "Signal Bloom", "sort-name": "Signal Bloom", "aliases": []any{map[string]any{"name": "The Signal Bloom", "locale": "en"}}, "tags": []any{map[string]any{"name": "Dream Pop", "count": 12}}},
		map[string]any{"id": "artist-2", "name": "Quiet Metric", "sort-name": "Quiet Metric", "genres": []any{map[string]any{"name": "Ambient", "count": 7}}},
	})
	recordings := make([]any, 0, 80)
	for i := range 80 {
		artistID, artistName := "artist-1", "Signal Bloom"
		if i%2 == 1 {
			artistID, artistName = "artist-2", "Quiet Metric"
		}
		recordings = append(recordings, map[string]any{
			"id": fmt.Sprintf("recording-%03d", i), "title": fmt.Sprintf("Track %03d %s", i, strings.Repeat(fmt.Sprintf("%x", i*7919+17), 12)),
			"length": 180000 + i, "first-release-date": "2025-04-03", "isrcs": []string{fmt.Sprintf("USTST25%05d", i)},
			"artist-credit": []any{map[string]any{"name": artistName, "artist": map[string]any{"id": artistID, "name": artistName, "sort-name": artistName}}},
			"tags":          []any{map[string]any{"name": "Shoegaze", "count": i + 1}},
		})
	}
	writeDump(t, recordingArchive, "recording", recordings)

	index := filepath.Join(root, "musicbrainz.sqlite")
	buildProgress := map[string]BuildProgress{}
	var buildProgressMu sync.Mutex
	info, err := Build(context.Background(), BuildOptions{
		Output: index, Snapshot: "20260912-001001", ArtistArchive: artistArchive, RecordingArchive: recordingArchive,
		Inputs: map[string]string{"artist.tar.xz": strings.Repeat("a", 64), "recording.tar.xz": strings.Repeat("b", 64)},
		Progress: func(update BuildProgress) {
			buildProgressMu.Lock()
			buildProgress[update.Entity] = update
			buildProgressMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Artists != 2 || info.Recordings != 80 || info.ArtistTags != 2 || info.RecordingTags != 80 {
		t.Fatalf("unexpected index counts: %+v", info)
	}
	for _, stage := range []string{"artists", "recordings", "finalize"} {
		update := buildProgress[stage]
		if update.Total <= 0 || update.Done != update.Total {
			t.Fatalf("incomplete %s build progress: %+v", stage, update)
		}
	}
	store, err := Open(index)
	if err != nil {
		t.Fatal(err)
	}
	artists, err := store.ArtistsByTag(context.Background(), []string{"dream pop"}, 10, 0)
	if err != nil || len(artists) != 1 || artists[0].MBID != "artist-1" {
		t.Fatalf("tag lookup: %+v, %v", artists, err)
	}
	tracks, err := store.ArtistRecordings(context.Background(), "artist-1", 5, 0)
	if err != nil || len(tracks) != 5 || len(tracks[0].Artists) != 1 || len(tracks[0].ISRCs) != 1 || len(tracks[0].Tags) != 1 {
		t.Fatalf("artist recordings: %+v, %v", tracks, err)
	}
	exact, err := store.FindRecordings(context.Background(), "The Signal Bloom", tracks[0].Title, 5)
	if err != nil || len(exact) != 1 || exact[0].FirstReleaseDate != "2025-04-03" {
		t.Fatalf("alias recording lookup: %+v, %v", exact, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	bundleDir := filepath.Join(root, "bundle")
	packageProgress := map[string]BundleProgress{}
	manifest, err := PackageWithProgress(context.Background(), index, bundleDir, 1024, func(update BundleProgress) {
		packageProgress[update.Stage] = update
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"hash-index", "compress-index"} {
		if update := packageProgress[stage]; update.Total <= 0 || update.Done != update.Total {
			t.Fatalf("incomplete %s progress: %+v", stage, update)
		}
	}
	if len(manifest.Parts) < 2 {
		t.Fatalf("expected a multipart archive, got %d part", len(manifest.Parts))
	}
	for _, part := range manifest.Parts {
		if part.Size > 1024 {
			t.Fatalf("oversized part: %+v", part)
		}
	}
	verifyProgress := map[string]BundleProgress{}
	if _, err = VerifyBundleWithProgress(context.Background(), bundleDir, func(update BundleProgress) {
		verifyProgress[update.Stage] = update
	}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"verify-parts", "verify-index"} {
		if update := verifyProgress[stage]; update.Total <= 0 || update.Done != update.Total {
			t.Fatalf("incomplete %s progress: %+v", stage, update)
		}
	}

	server := httptest.NewServer(http.FileServer(http.Dir(bundleDir)))
	defer server.Close()
	installDir := filepath.Join(root, "installed")
	installed, err := Install(context.Background(), server.URL+"/musicbrainz-manifest.json", installDir, ports.NopProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if installed != ActivePath(installDir) {
		t.Fatalf("active index %q, installed %q", ActivePath(installDir), installed)
	}
	installedStore, err := Open(installed)
	if err != nil {
		t.Fatal(err)
	}
	defer installedStore.Close()
	if got := installedStore.Info(); got.Snapshot != info.Snapshot || got.Recordings != info.Recordings {
		t.Fatalf("installed info mismatch: %+v", got)
	}
	for _, part := range manifest.Parts {
		if _, err := os.Stat(filepath.Join(installDir, strings.ToLower(part.SHA256)+".part")); !os.IsNotExist(err) {
			t.Fatalf("installer part retained after activation: %s (%v)", part.Name, err)
		}
	}
}

func TestBundleManifestRejectsUnsafeOrOversizedParts(t *testing.T) {
	t.Parallel()
	base := BundleManifest{
		Version: BundleVersion, IndexVersion: IndexVersion, Snapshot: "20260912-001001", CoreLicense: "CC0-1.0", TagsLicense: "CC-BY-NC-SA-3.0",
		Index: Artifact{Name: "musicbrainz.sqlite", Size: 1, SHA256: strings.Repeat("a", 64)},
		Parts: []Artifact{{Name: "musicbrainz.sqlite.zst.part-00001", Size: 1, SHA256: strings.Repeat("b", 64)}},
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	unsafe := base
	unsafe.Parts = append([]Artifact(nil), base.Parts...)
	unsafe.Parts[0].Name = "../musicbrainz.sqlite.zst.part-00001"
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe part path accepted")
	}
	oversized := base
	oversized.Parts = append([]Artifact(nil), base.Parts...)
	oversized.Parts[0].Size = 200_000_001
	if err := oversized.Validate(); err == nil {
		t.Fatal("oversized part accepted")
	}
}

func TestParseSHA256Sums(t *testing.T) {
	t.Parallel()
	got := parseSHA256Sums(strings.Repeat("A", 64) + "  artist.tar.xz\n" + strings.Repeat("b", 64) + " *recording.tar.xz\ninvalid")
	if got["artist.tar.xz"] != strings.Repeat("a", 64) || got["recording.tar.xz"] != strings.Repeat("b", 64) {
		t.Fatal(got)
	}
}

func TestDownloadRunsArchivesInParallelAndReusesVerifiedFiles(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"artist.tar.xz":    []byte("artist archive"),
		"recording.tar.xz": []byte("recording archive"),
	}
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(files[strings.TrimPrefix(r.URL.Path, "/")])
	}))
	set := DumpSet{Snapshot: "20260912-001001", Files: map[string]DumpFile{}}
	for name, body := range files {
		sum := sha256.Sum256(body)
		set.Files[name] = DumpFile{Name: name, URL: server.URL + "/" + name, Size: int64(len(body)), SHA256: fmt.Sprintf("%x", sum)}
	}
	go func() {
		<-arrived
		<-arrived
		close(release)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	downloaded, err := Download(ctx, set, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	for name := range files {
		if downloaded.Files[name].Path == "" {
			t.Fatalf("missing local path for %s", name)
		}
	}
	// A second run succeeds with the source offline because verified archives
	// are reused instead of downloaded again.
	if _, err = Download(ctx, set, dir, nil); err != nil {
		t.Fatalf("reuse verified files: %v", err)
	}
}

func TestReadJSONDumpRequiresMatchingEntityMember(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "artist.tar.xz")
	writeDump(t, archive, "recording", []any{map[string]any{"id": "recording-1"}})

	err := readJSONDump(context.Background(), archive, "artists", "artist", nil, func(json.RawMessage) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "missing mbdump/artist") {
		t.Fatalf("expected missing artist member error, got %v", err)
	}
}

func TestReadJSONDumpStreamsRowsLargerThanSixteenMiB(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "artist.tar.xz")
	writeDump(t, archive, "artist", []any{map[string]any{
		"id": "artist-large", "name": "Large Artist", "disambiguation": strings.Repeat("x", (16<<20)+1),
	}})

	var rows int
	err := readJSONDump(context.Background(), archive, "artists", "artist", nil, func(raw json.RawMessage) error {
		rows++
		if len(raw) <= 16<<20 {
			t.Fatalf("fixture row was only %d bytes", len(raw))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("read %d rows, want 1", rows)
	}
}

func TestBundleRedirectRejectsInsecureTarget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/musicbrainz-manifest.json", http.StatusFound)
	}))
	defer server.Close()
	if _, err := LoadBundle(context.Background(), server.URL+"/musicbrainz-manifest.json"); err == nil {
		t.Fatal("insecure redirect accepted")
	}
}

func writeDump(t testing.TB, path, entity string, rows []any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	xw, err := xz.NewWriter(f)
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	writeMember := func(name, value string) {
		t.Helper()
		if err != nil {
			return
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(value))}); err == nil {
			_, err = tw.Write([]byte(value))
		}
	}
	// Official JSON dumps contain control files before the mbdump/<entity> JSONL
	// member. The timestamp is deliberately not valid JSON, matching production.
	writeMember("TIMESTAMP", "2026-09-12 00:10:01.000000+00\n")
	writeMember("SCHEMA_SEQUENCE", "31\n")
	var content strings.Builder
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		content.Write(raw)
		content.WriteByte('\n')
	}
	writeMember("mbdump/"+entity, content.String())
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := xw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}
