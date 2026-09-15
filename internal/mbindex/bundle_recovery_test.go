package mbindex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/platten/playlistai/internal/installlock"
)

func recoveryBundle(t *testing.T) (string, BundleManifest) {
	return recoveryBundleSnapshot(t, "20260912-001001")
}

func recoveryBundleSnapshot(t *testing.T, snapshot string) (string, BundleManifest) {
	t.Helper()
	root := t.TempDir()
	artist := filepath.Join(root, "artist.tar.xz")
	recording := filepath.Join(root, "recording.tar.xz")
	writeDump(t, artist, "artist", []any{map[string]any{"id": "artist-1", "name": "Fixture Artist"}})
	writeDump(t, recording, "recording", []any{map[string]any{"id": "recording-1", "title": "Fixture Track", "artist-credit": []any{map[string]any{"name": "Fixture Artist", "artist": map[string]any{"id": "artist-1", "name": "Fixture Artist"}}}}})
	index := filepath.Join(root, "index.sqlite")
	if _, err := Build(context.Background(), BuildOptions{Output: index, ArtistArchive: artist, RecordingArchive: recording, Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bundle")
	manifest, err := Package(context.Background(), index, dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return dir, manifest
}

func TestInstallRecoversLegacyMarkerAndCorruptOpenTarget(t *testing.T) {
	bundle, m := recoveryBundle(t)
	var parts atomic.Int64
	files := http.FileServer(http.Dir(bundle))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ".part-") {
			parts.Add(1)
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	oldName := "musicbrainz-" + m.Index.SHA256 + ".sqlite"
	oldPath := filepath.Join(dir, oldName)
	if err := os.WriteFile(oldPath, []byte("corrupt prior database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := activate(dir, oldName); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	installed, err := Install(context.Background(), server.URL+"/musicbrainz-manifest.json", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if installed == oldPath || ActivePath(dir) != installed {
		t.Fatalf("repair not activated: %s", installed)
	}
	if err := verifyArtifact(context.Background(), installed, m.Index); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(oldPath); err != nil || string(raw) != "corrupt prior database" {
		t.Fatalf("old reader target changed: %q %v", raw, err)
	}
	before := parts.Load()
	retry, err := Install(context.Background(), server.URL+"/musicbrainz-manifest.json", dir, nil)
	if err != nil || retry != installed || parts.Load() != before {
		t.Fatalf("repaired active file not reused: %s %v", retry, err)
	}
}

func TestInstallRejectsLiveOwner(t *testing.T) {
	bundle, _ := recoveryBundle(t)
	server := httptest.NewServer(http.FileServer(http.Dir(bundle)))
	defer server.Close()
	dir := t.TempDir()
	release, err := installlock.TryAcquire(filepath.Join(dir, "install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	_, err = Install(context.Background(), server.URL+"/musicbrainz-manifest.json", dir, nil)
	if !errors.Is(err, installlock.ErrBusy) {
		t.Fatalf("concurrent owner accepted: %v", err)
	}
}

func TestInstallFailurePreservesActiveAndRemovesOwnedStage(t *testing.T) {
	bundle, m := recoveryBundle(t)
	previousBundle, _ := recoveryBundleSnapshot(t, "20260911-001001")
	previous, err := os.ReadFile(filepath.Join(filepath.Dir(previousBundle), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"download-failed", "canceled", "activation-failed"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			healthy := filepath.Join(dir, "musicbrainz-previous.sqlite")
			if err := os.WriteFile(healthy, previous, 0600); err != nil {
				t.Fatal(err)
			}
			if err := activate(dir, filepath.Base(healthy)); err != nil {
				t.Fatal(err)
			}
			oldPointer, err := os.ReadFile(filepath.Join(dir, "active"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			files := http.FileServer(http.Dir(bundle))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, ".part-") {
					if scenario == "download-failed" {
						http.Error(w, "unavailable", http.StatusBadRequest)
						return
					}
					if scenario == "canceled" {
						cancel()
						return
					}
				}
				files.ServeHTTP(w, r)
			}))
			defer server.Close()
			// A directory prevents activation without using permission behavior that
			// differs between administrator and normal-user test runners.
			if scenario == "activation-failed" {
				if err := os.Remove(filepath.Join(dir, "active")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "active"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Install(ctx, server.URL+"/musicbrainz-manifest.json", dir, nil); err == nil {
				t.Fatal("failure accepted")
			}
			if raw, err := os.ReadFile(healthy); err != nil || string(raw) != string(previous) {
				t.Fatal("prior snapshot changed")
			}
			store, err := Open(healthy)
			if err != nil {
				t.Fatalf("prior snapshot no longer opens: %v", err)
			}
			if store.Info().Snapshot != "20260911-001001" {
				t.Error("prior snapshot identity changed")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if scenario != "activation-failed" {
				if raw, err := os.ReadFile(filepath.Join(dir, "active")); err != nil || string(raw) != string(oldPointer) {
					t.Fatal("prior activation changed")
				}
			}
			stages, _ := filepath.Glob(filepath.Join(dir, "musicbrainz-"+m.Index.SHA256+"-*.sqlite"))
			if len(stages) > 0 {
				t.Fatalf("failed target retained: %v", stages)
			}
		})
	}
}

func TestPublishBuildPreservesPreviousOnPromotionFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.sqlite")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishBuild(filepath.Join(dir, "missing-stage"), target, true); err == nil {
		t.Fatal("missing stage accepted")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "previous" {
		t.Fatal("previous output deleted")
	}
	stage := filepath.Join(dir, "stage.sqlite")
	if err := os.WriteFile(stage, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishBuild(stage, target, false); err == nil {
		t.Fatal("non-replace overwrote concurrent output")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "previous" {
		t.Fatal("previous output changed")
	}
	if err := publishBuild(stage, target, true); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "new" {
		t.Fatal("replacement missing")
	}
}
