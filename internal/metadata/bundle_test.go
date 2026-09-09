package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func compactFixture(t *testing.T) (string, string, BundleManifest) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.sqlite")
	_, err := Build(context.Background(), BuildOptions{Output: source, Date: "20260901", CatalogVersion: "fixture", Tracks: []core.TrackRef{{ID: "a", Artist: "Artist", Title: "Song"}, {ID: "b", Artist: "Rock Artist", Title: "Rock Song"}}, Inputs: []Input{fixtureInput(t, fixtureReleases)}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "upload")
	m, err := Package(context.Background(), source, dir)
	if err != nil {
		t.Fatal(err)
	}
	return source, dir, m
}

func TestCompactPreservesRuntimeEvidence(t *testing.T) {
	source, dir, m := compactFixture(t)
	s, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := Open(filepath.Join(dir, m.Index.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Info().Version != RuntimeVersion || c.Info().Tracks != s.Info().Tracks {
		t.Fatal(c.Info())
	}
	for _, genre := range []string{"Electronic", "Ambient", "Rock", "Unknown"} {
		original, _ := s.Genre(context.Background(), genre, 100)
		for _, pivot := range []uint32{0, 42, 1<<32 - 1} {
			got, err := c.SampleGenre(context.Background(), genre, 100, pivot)
			if err != nil || !reflect.DeepEqual(original, got) {
				t.Fatal(genre, pivot, original, got, err)
			}
		}
		if c.HasGenre(context.Background(), genre) != s.HasGenre(context.Background(), genre) {
			t.Fatal("genre coverage changed")
		}
	}
	if m.Archive.Size >= m.Index.Size {
		t.Fatal("fixture did not compress")
	}
	var count int
	if err := c.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('entities','entity_tracks','genre_sample','recording_join','credit_join')").Scan(&count); err != nil || count != 0 {
		t.Fatal("archival/temporary indexes shipped", count, err)
	}
}

func TestBundleInstallIntegrityAndOfflineActivation(t *testing.T) {
	_, dir, m := compactFixture(t)
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zst") {
			downloads.Add(1)
		}
		http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
	}))
	defer server.Close()
	source := server.URL + "/metadata-manifest.json"
	dest := t.TempDir()
	if _, err := Install(context.Background(), source, dest, "wrong-catalog", nil); err == nil || downloads.Load() != 0 {
		t.Fatal("wrong catalog downloaded")
	}
	installed, err := Install(context.Background(), source, dest, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ActivePath(dest) != installed {
		t.Fatal("active pointer missing")
	}
	got, err := artifact(context.Background(), installed)
	if err != nil || got.SHA256 != m.Index.SHA256 || downloads.Load() != 1 {
		t.Fatal(got, err)
	}
	if _, err = Install(context.Background(), source, dest, "fixture", nil); err != nil || downloads.Load() != 1 {
		t.Fatal("installed bundle re-downloaded", err)
	}
	if _, err = os.Stat(filepath.Join(dest, m.Archive.SHA256+".zst")); !os.IsNotExist(err) {
		t.Fatal("compressed installer cache retained")
	}
	// Runtime opens the installed database without needing the host or archive.
	server.Close()
	s, err := Open(ActivePath(dest))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.HasGenre(context.Background(), "Electronic") {
		t.Fatal("offline runtime unavailable")
	}
}

func TestBundleRejectsCorruptionAndTraversal(t *testing.T) {
	_, dir, m := compactFixture(t)
	for _, mode := range []string{"archive_hash", "index_hash", "expanded_size", "catalog_header", "traversal", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			bad := m
			switch mode {
			case "archive_hash":
				bad.Archive.SHA256 = strings.Repeat("0", 64)
			case "index_hash":
				bad.Index.SHA256 = strings.Repeat("0", 64)
			case "expanded_size":
				bad.Index.Size = 1
			case "catalog_header":
				bad.Catalog = "different"
			case "traversal":
				bad.Archive.Name = "../escape.zst"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".json") {
					_ = json.NewEncoder(w).Encode(bad)
					return
				}
				http.ServeFile(w, r, filepath.Join(dir, m.Archive.Name))
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			dest := t.TempDir()
			if _, err := Install(ctx, server.URL+"/metadata-manifest.json", dest, bad.Catalog, nil); err == nil {
				t.Fatal("invalid bundle installed")
			}
			if _, err := os.Stat(filepath.Join(dest, "active")); !os.IsNotExist(err) {
				t.Fatal("failed install activated")
			}
		})
	}
}
