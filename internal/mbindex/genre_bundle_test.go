package mbindex

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/genrevocab"
)

func TestPackageBundleIncludesAndVerifiesGenreVocabulary(t *testing.T) {
	index := genreBundleIndex(t)
	vocabulary := filepath.Join(t.TempDir(), "prepared.json")
	writeGenreVocabulary(t, vocabulary, "Liquid drum and bass")
	dir := filepath.Join(t.TempDir(), "bundle")

	manifest, err := PackageBundle(context.Background(), BundlePackageOptions{
		Index: index, GenreVocabulary: vocabulary, Directory: dir, PartBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.GenreVocabulary == nil || manifest.GenreVocabulary.Name != GenreVocabularyName {
		t.Fatalf("genre artifact missing: %+v", manifest)
	}
	if _, err := genrevocab.Load(filepath.Join(dir, GenreVocabularyName)); err != nil {
		t.Fatalf("packaged vocabulary: %v", err)
	}
	verified, err := VerifyBundle(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if verified.GenreVocabulary == nil || verified.GenreVocabulary.SHA256 != manifest.GenreVocabulary.SHA256 {
		t.Fatalf("verified manifest lost genre artifact: %+v", verified)
	}

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"genres":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PackageBundle(context.Background(), BundlePackageOptions{
		Index: index, GenreVocabulary: invalid, Directory: filepath.Join(t.TempDir(), "invalid-bundle"), PartBytes: 1024,
	}); err == nil || !strings.Contains(err.Error(), "genre vocabulary") {
		t.Fatalf("invalid genre vocabulary accepted: %v", err)
	}
}

func TestInstallUpdatesGenreVocabularyWithoutDownloadingUnchangedIndex(t *testing.T) {
	index := genreBundleIndex(t)
	firstBundle, firstManifest := packageGenreFixture(t, index, "Ambient")
	firstServer := httptest.NewServer(http.FileServer(http.Dir(firstBundle)))
	installDir := t.TempDir()
	installed, err := Install(context.Background(), firstServer.URL+"/musicbrainz-manifest.json", installDir, nil)
	firstServer.Close()
	if err != nil {
		t.Fatal(err)
	}
	if ActivePath(installDir) != installed || ActiveGenreHash(installDir) != strings.ToLower(firstManifest.GenreVocabulary.SHA256) {
		t.Fatalf("initial activation index=%q genre=%q", ActivePath(installDir), ActiveGenreHash(installDir))
	}
	firstGenrePath := ActiveGenrePath(installDir)

	secondBundle, secondManifest := packageGenreFixture(t, index, "Liquid drum and bass")
	var indexPartRequests atomic.Int64
	files := http.FileServer(http.Dir(secondBundle))
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ".part-") {
			indexPartRequests.Add(1)
		}
		files.ServeHTTP(w, r)
	}))
	updatedIndex, err := Install(context.Background(), secondServer.URL+"/musicbrainz-manifest.json", installDir, nil)
	secondServer.Close()
	if err != nil {
		t.Fatal(err)
	}
	if updatedIndex != installed || indexPartRequests.Load() != 0 {
		t.Fatalf("unchanged index was downloaded: installed=%q requests=%d", updatedIndex, indexPartRequests.Load())
	}
	if ActiveGenrePath(installDir) == firstGenrePath || ActiveGenreHash(installDir) != strings.ToLower(secondManifest.GenreVocabulary.SHA256) {
		t.Fatalf("genre update not independently activated: path=%q hash=%q", ActiveGenrePath(installDir), ActiveGenreHash(installDir))
	}
	vocabulary, err := genrevocab.Load(ActiveGenrePath(installDir))
	if err != nil || len(vocabulary.Genres) != 1 || vocabulary.Genres[0].Name != "Liquid drum and bass" {
		t.Fatalf("active vocabulary: %+v %v", vocabulary, err)
	}

	stablePath, stableHash := ActiveGenrePath(installDir), ActiveGenreHash(installDir)
	for _, scenario := range []string{"damaged", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			updateIndex := index
			if scenario == "damaged" {
				updateIndex = genreBundleIndexAt(t, "20260913-001001", "Changed Fixture Track")
			}
			bundle, manifest := packageGenreFixture(t, updateIndex, "Genre "+scenario)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			files := http.FileServer(http.Dir(bundle))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if filepath.Base(r.URL.Path) == manifest.GenreVocabulary.Name {
					if scenario == "canceled" {
						cancel()
						return
					}
					_, _ = w.Write(bytes.Repeat([]byte("x"), int(manifest.GenreVocabulary.Size)))
					return
				}
				files.ServeHTTP(w, r)
			}))
			defer server.Close()
			if _, err := Install(ctx, server.URL+"/musicbrainz-manifest.json", installDir, nil); err == nil {
				t.Fatal("failed genre update accepted")
			}
			if ActiveGenrePath(installDir) != stablePath || ActiveGenreHash(installDir) != stableHash {
				t.Fatalf("failed update changed active vocabulary: path=%q hash=%q", ActiveGenrePath(installDir), ActiveGenreHash(installDir))
			}
			if ActivePath(installDir) != installed {
				t.Fatalf("failed genre update activated a different index: got %q want %q", ActivePath(installDir), installed)
			}
		})
	}
}

func packageGenreFixture(t *testing.T, index, name string) (string, BundleManifest) {
	t.Helper()
	root := t.TempDir()
	vocabulary := filepath.Join(root, "prepared.json")
	writeGenreVocabulary(t, vocabulary, name)
	dir := filepath.Join(root, "bundle")
	manifest, err := PackageBundle(context.Background(), BundlePackageOptions{
		Index: index, GenreVocabulary: vocabulary, Directory: dir, PartBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, manifest
}

func writeGenreVocabulary(t *testing.T, path, name string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"genre-count":1,"genres":[{"id":"genre-id","name":%q}]}`, name)
	}))
	defer server.Close()
	vocabulary, err := genrevocab.Fetch(context.Background(), server.Client(), server.URL, "PlaylistAI/test", time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := genrevocab.Write(path, vocabulary); err != nil {
		t.Fatal(err)
	}
}

func genreBundleIndex(t *testing.T) string {
	return genreBundleIndexAt(t, "20260912-001001", "Fixture Track")
}

func genreBundleIndexAt(t *testing.T, snapshot, title string) string {
	t.Helper()
	root := t.TempDir()
	artist := filepath.Join(root, "artist.tar.xz")
	recording := filepath.Join(root, "recording.tar.xz")
	writeDump(t, artist, "artist", []any{map[string]any{"id": "artist-1", "name": "Fixture Artist"}})
	writeDump(t, recording, "recording", []any{map[string]any{
		"id": "recording-1", "title": title,
		"artist-credit": []any{map[string]any{"name": "Fixture Artist", "artist": map[string]any{"id": "artist-1", "name": "Fixture Artist"}}},
	}})
	index := filepath.Join(root, "index.sqlite")
	if _, err := Build(context.Background(), BuildOptions{
		Output: index, ArtistArchive: artist, RecordingArchive: recording, Snapshot: snapshot,
	}); err != nil {
		t.Fatal(err)
	}
	return index
}
