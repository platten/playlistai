package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/musicgraph"
)

func graphFixture(t *testing.T, day int) (string, string, musicgraph.Snapshot) {
	t.Helper()
	s := musicgraph.Snapshot{Version: musicgraph.Version, PreparedAt: time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "input.json")
	hash, err := musicgraph.Write(context.Background(), path, s)
	if err != nil {
		t.Fatal(err)
	}
	return path, hash, s
}

func TestPreparedMusicGraphActivationAndHistoricalPin(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	ctx := context.Background()
	if reader, err := c.PreparedMusicGraph(ctx); err != nil || reader != nil {
		t.Fatal(reader, err)
	}
	first, firstHash, _ := graphFixture(t, 1)
	status, err := c.ImportMusicGraph(ctx, first, firstHash)
	if err != nil || !status.Installed || status.Snapshot != firstHash {
		t.Fatal(status, err)
	}
	pinned, err := c.PreparedMusicGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, _ := graphFixture(t, 2)
	if _, err = c.ImportMusicGraph(ctx, second, secondHash); err != nil {
		t.Fatal(err)
	}
	active, err := c.PreparedMusicGraph(ctx)
	if err != nil || active.SnapshotIdentity() != secondHash || pinned.SnapshotIdentity() != firstHash {
		t.Fatal(active, err)
	}
	prior, err := c.PreparedMusicGraphSnapshot(ctx, firstHash)
	if err != nil || prior.SnapshotIdentity() != firstHash {
		t.Fatal(prior, err)
	}
	if _, err = c.PreparedMusicGraphSnapshot(ctx, strings.Repeat("f", 64)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing historical hash silently fell back", err)
	}
	if _, err = c.ImportMusicGraph(ctx, first, secondHash); err == nil {
		t.Fatal("corrupt import accepted")
	}
	after, err := c.PreparedMusicGraph(ctx)
	if err != nil || after.SnapshotIdentity() != secondHash {
		t.Fatal("failed import damaged activation", err)
	}
	// A fresh process verifies the retained artifact, not the previous cache.
	fresh := &Container{cfg: c.cfg}
	r, err := fresh.PreparedMusicGraph(ctx)
	if err != nil || r.SnapshotIdentity() != secondHash {
		t.Fatal(r, err)
	}
}

func TestPreparedMusicGraphCancellationLockAndInvalidActivation(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	path, hash, _ := graphFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ImportMusicGraph(ctx, path, hash); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.graphDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled import created data")
	}
	release, err := c.graphInstallLock()
	if err != nil {
		t.Fatal(err)
	}
	other := &Container{cfg: c.cfg}
	if _, err = other.ImportMusicGraph(context.Background(), path, hash); err == nil {
		t.Fatal("concurrent installation accepted")
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(c.graphDir(), "active.json"), []byte(`{"version":"prepared-music-graph/v1","sha256":"../../private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PreparedMusicGraph(context.Background()); err == nil {
		t.Fatal("unsafe activation accepted")
	}
}

type graphTestTransport func(*http.Request) (*http.Response, error)

func (f graphTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInstallMusicGraphUsesVerifiedDownloadAndKeepsOldOnFailure(t *testing.T) {
	path, hash, s := graphFixture(t, 1)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := MusicGraphManifest{Version: musicgraph.Version, PreparedAt: s.PreparedAt, Snapshot: MusicGraphArtifact{Name: graphName(hash), Size: int64(len(body)), SHA256: hash, URL: "https://assets.example.org/graph.json"}}
	bad := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/manifest.json" {
			_ = json.NewEncoder(w).Encode(manifest)
			return
		}
		if r.URL.Path != "/graph.json" {
			t.Error(r.URL)
			w.WriteHeader(404)
			return
		}
		if bad {
			fmt.Fprint(w, "corrupt")
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := graphDownloadClient()
	client.Transport = graphTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "assets.example.org" {
			t.Error("unexpected target", r.URL)
		}
		copyReq := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		copyReq.URL = &u
		return transport.RoundTrip(copyReq)
	})
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	status, err := c.installMusicGraph(context.Background(), "https://assets.example.org/manifest.json", nil, client)
	if err != nil || !status.Installed || status.Snapshot != hash || calls != 2 {
		t.Fatal(status, err, calls)
	}
	bad = true
	if _, err = c.installMusicGraph(context.Background(), "https://assets.example.org/manifest.json", nil, client); err == nil {
		t.Fatal("corrupt download accepted")
	}
	reader, err := c.PreparedMusicGraph(context.Background())
	if err != nil || reader.SnapshotIdentity() != hash {
		t.Fatal("failed update lost good artifact", err)
	}
	entries, _ := os.ReadDir(c.graphDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".graph-") {
			t.Fatal("staging leaked", e.Name())
		}
	}
}

func TestMusicGraphManifestRejectsUnsafeSource(t *testing.T) {
	for _, u := range []string{"http://example.org/a", "https://user:secret@example.org/a", "https://example.org/a?token=secret", "https://127.0.0.1/a", "https://127.1/a", "https://2130706433/a", "https://localhost/a"} {
		if graphPublicURL(u) {
			t.Fatal("unsafe source accepted", u)
		}
	}
}
