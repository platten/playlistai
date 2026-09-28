package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/ports"
)

// Run explicitly with original pinned assets, a verified installed CLAP bundle,
// and a freshly built desktop binary. Normal regressions stay small and offline.
func TestDiscogsNativeInstallOptIn(t *testing.T) {
	models, runtimeDir, binary := os.Getenv("PLAYLISTAI_DISCOGS_MODELS"), os.Getenv("PLAYLISTAI_DISCOGS_RUNTIME"), os.Getenv("PLAYLISTAI_DISCOGS_BINARY")
	if models == "" || runtimeDir == "" || binary == "" {
		t.Skip("native model assets and desktop binary not provided")
	}
	root := t.TempDir()
	cache := filepath.Join(root, "model-downloads", "discogs-effnet-v1")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	for _, asset := range audio.DiscogsFiles {
		from, err := os.Open(filepath.Join(models, asset.Name))
		if err != nil {
			t.Fatal(err)
		}
		to, err := os.OpenFile(filepath.Join(cache, asset.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = from.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(to, from)
		if err = errors.Join(err, from.Close(), to.Close()); err != nil {
			t.Fatal(err)
		}
	}
	c := &Container{cfg: config.Config{DataDir: root}}
	c.analysis.worker = &audio.Worker{BundleDir: runtimeDir}
	c.analysis.manifest = &audio.BundleManifest{}
	c.discogs.workerExecutable = binary
	if err := c.InstallDiscogs(context.Background(), ports.NopProgress{}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.RemoveDiscogs() }()
	status, err := c.GetDiscogsStatus(context.Background())
	if err != nil || !status.Installed || !status.Available {
		t.Fatalf("native install status=%+v err=%v", status, err)
	}
	if err := c.RemoveDiscogs(); err != nil {
		t.Fatal(err)
	}
	status, err = c.GetDiscogsStatus(context.Background())
	if err != nil || status.Installed || status.Available {
		t.Fatalf("native removal status=%+v err=%v", status, err)
	}
}
