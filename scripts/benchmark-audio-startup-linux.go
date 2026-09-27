//go:build linux

// Run with: go run scripts/benchmark-audio-startup-linux.go
// Read-only reader timings for installed CLAP and MERT bundles. This does not
// measure worker health, native window paint, or Generate readiness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/platten/playlistai/internal/audio"
)

func evict(dir string, artifacts []audio.BundleArtifact) {
	for _, artifact := range artifacts {
		for _, name := range []string{artifact.Name, filepath.Base(artifact.ArchiveMember)} {
			if name == "." {
				continue
			}
			file, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			_ = unix.Fadvise(int(file.Fd()), 0, 0, unix.FADV_DONTNEED)
			_ = file.Close()
		}
	}
}

func measure(label, dir string, artifacts []audio.BundleArtifact, full, layout func(context.Context, string) error) error {
	for _, mode := range []string{"cache-evicted", "warm"} {
		for i := 1; i <= 3; i++ {
			if mode == "cache-evicted" {
				evict(dir, artifacts)
			}
			start := time.Now()
			if err := full(context.Background(), dir); err != nil {
				return err
			}
			verified := time.Since(start)
			if mode == "cache-evicted" {
				evict(dir, artifacts)
			}
			start = time.Now()
			if err := layout(context.Background(), dir); err != nil {
				return err
			}
			fmt.Printf("%s %s %d full=%s layout=%s\n", label, mode, i, verified, time.Since(start))
		}
	}
	return nil
}

func main() {
	defaultDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dataDir := flag.String("data-dir", filepath.Join(defaultDir, "playlist-ai"), "installed model data directory")
	flag.Parse()
	clap := &audio.BundleManager{Directory: filepath.Join(*dataDir, "music-analysis")}
	clapDir, clapManifest, err := clap.ActiveStartupContext(context.Background())
	if err == nil {
		err = measure("CLAP", clapDir, clapManifest.Artifacts,
			func(ctx context.Context, dir string) error { _, err := audio.ReadBundleContext(ctx, dir); return err },
			func(ctx context.Context, dir string) error {
				_, err := audio.ReadStartupBundleContext(ctx, dir)
				return err
			})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "CLAP:", err)
		os.Exit(1)
	}
	mert := &audio.MERTBundleManager{Directory: filepath.Join(*dataDir, "mert-analysis")}
	mertDir, mertManifest, err := mert.ActiveStartupContext(context.Background())
	if err == nil {
		err = measure("MERT", mertDir, mertManifest.Artifacts,
			func(ctx context.Context, dir string) error {
				_, err := audio.ReadMERTBundleContext(ctx, dir)
				return err
			},
			func(ctx context.Context, dir string) error {
				_, err := audio.ReadStartupMERTBundleContext(ctx, dir)
				return err
			})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "MERT:", err)
		os.Exit(1)
	}
}
