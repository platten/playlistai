package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/libraryindex"
)

type scriptedMERTWarmer struct {
	errors []error
	calls  int
}

func (w *scriptedMERTWarmer) Warm(context.Context) error {
	index := min(w.calls, len(w.errors)-1)
	w.calls++
	return w.errors[index]
}

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

func TestParseAppendRootRequiresStableAlias(t *testing.T) {
	alias, path, err := parseNamedRoot("--append-root", " archive = /mnt/second library ")
	if err != nil || alias != "archive" || path != "/mnt/second library" {
		t.Fatalf("parse append root: alias=%q path=%q err=%v", alias, path, err)
	}
	if _, _, err := parseNamedRoot("--append-root", "/mnt/without-alias"); err == nil || !strings.Contains(err.Error(), "ALIAS=PATH") {
		t.Fatalf("invalid append root error = %v", err)
	}
}

func TestMERTWarmupRetriesTransientNativeFailure(t *testing.T) {
	warmer := &scriptedMERTWarmer{errors: []error{audio.ErrNativeWorker, audio.ErrNativeWorker, nil}}
	var log bytes.Buffer
	var issues []libraryindex.ProcessingIssue
	if err := warmMERTWithRetries(context.Background(), warmer, &log, func(issue libraryindex.ProcessingIssue) { issues = append(issues, issue) }); err != nil {
		t.Fatal(err)
	}
	if warmer.calls != 3 || strings.Count(log.String(), "restarting native sessions") != 2 || len(issues) != 2 || issues[0].Code != "native_worker_restart" {
		t.Fatalf("calls=%d log=%q issues=%+v", warmer.calls, log.String(), issues)
	}
	permanent := &scriptedMERTWarmer{errors: []error{errors.New("invalid model")}}
	if err := warmMERTWithRetries(context.Background(), permanent, io.Discard, nil); err == nil || permanent.calls != 1 {
		t.Fatalf("permanent failure was retried: calls=%d err=%v", permanent.calls, err)
	}
}

func TestConfigDefaultsAndCLIPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexer.json")
	if err := os.WriteFile(path, []byte(`{"concurrency":"manual","workers":"4","ioWorkers":1,"inferenceThreads":1,"maxRam":"4GiB","seed":7,"followDirectorySymlinks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var values commonFlags
	args := []string{"--config", path, "--workers", "2", "--follow-directory-symlinks=false"}
	if err := values.preloadConfig(args); err != nil {
		t.Fatal(err)
	}
	if !values.followDirectorySymlinks {
		t.Fatal("configuration did not enable directory symlinks")
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
	if plan.HeavyWorkers != 2 || plan.IOWorkers != 1 || values.seed != 7 || values.followDirectorySymlinks {
		t.Fatalf("CLI/config precedence failed: %+v seed=%d", plan, values.seed)
	}
}
