package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestCLIValidationAndSizeUnits(t *testing.T) {
	if got, err := parseSize("8GiB"); err != nil || got != 8<<30 {
		t.Fatalf("size=%d err=%v", got, err)
	}
	if _, err := parseSize("8"); err == nil {
		t.Fatal("unitless size accepted")
	}
	var out, stderr bytes.Buffer
	code, err := execute(context.Background(), []string{"scan", "--concurrency", "serial", "--decode-workers", "2", "--root", t.TempDir()}, &out, &stderr)
	if err == nil || code != 1 || !strings.Contains(err.Error(), "serial mode conflicts") {
		t.Fatalf("code=%d err=%v stderr=%s", code, err, stderr.String())
	}
}

func TestDefaultRootAliasKeepsUnusualPathsPackSafe(t *testing.T) {
	for input, want := range map[string]string{
		"/mnt/Music Library": "Music-Library",
		"/mnt/音楽":            "root",
		"/mnt/AC_DC":         "AC_DC",
	} {
		if got := defaultRootAlias(input); got != want {
			t.Errorf("defaultRootAlias(%q)=%q want %q", input, got, want)
		}
	}
}

func TestConfigDefaultsAndCLIPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexer.json")
	if err := os.WriteFile(path, []byte(`{"concurrency":"manual","workers":"4","ioWorkers":1,"inferenceThreads":1,"maxRam":"4GiB","seed":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var values commonFlags
	args := []string{"--config", path, "--workers", "2"}
	if err := values.preloadConfig(args); err != nil {
		t.Fatal(err)
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	addCommon(flags, &values)
	if err := flags.Parse(args); err != nil {
		t.Fatal(err)
	}
	plan, err := values.plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.HeavyWorkers != 2 || plan.IOWorkers != 1 || values.seed != 7 {
		t.Fatalf("CLI/config precedence failed: %+v seed=%d", plan, values.seed)
	}
}

func TestPipelineAnalysisLifecycleJoinsBeforeStateCloseOnRootFailure(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	state, err := libraryindex.OpenState(context.Background(), stateDir, "lifecycle-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	musicRoot := filepath.Join(t.TempDir(), "music")
	accepted := make(chan error, 1)
	finished := make(chan struct{})
	lifecycle := startPipelineAnalysis(context.Background(), func(ctx context.Context, discoveryDone <-chan struct{}) (libraryindex.AnalysisReport, error) {
		_, err := state.EnsureRoot(ctx, musicRoot, "music")
		accepted <- err
		if err != nil {
			close(finished)
			return libraryindex.AnalysisReport{}, err
		}
		<-discoveryDone
		<-ctx.Done()
		close(finished)
		return libraryindex.AnalysisReport{}, ctx.Err()
	})
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	rootFailure := errors.New("root enumeration failed")
	if _, err := lifecycle.finish(rootFailure); !errors.Is(err, rootFailure) || !errors.Is(err, context.Canceled) {
		t.Fatalf("joined error = %v", err)
	}
	// Repeated completion after finish exercises the one-time discovery barrier.
	lifecycle.completeDiscovery()
	lifecycle.completeDiscovery()
	select {
	case <-finished:
	default:
		t.Fatal("finish returned before the analyzer exited")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := libraryindex.OpenState(context.Background(), stateDir, "lifecycle-verify", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var roots int
	if err := reopened.Reader().QueryRow(`SELECT COUNT(*) FROM roots WHERE alias='music'`).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if roots != 1 {
		t.Fatalf("accepted writer commit was not durable: roots=%d", roots)
	}
}
