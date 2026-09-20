package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/librarypack"
)

func args(t *testing.T, values ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"recoeval"}, values...)
	t.Cleanup(func() { os.Args = old })
}

func TestPaipackBlindDefaultsUseAvailableVariants(t *testing.T) {
	dir := t.TempDir()
	pack := filepath.Join(dir, "library.paipack")
	if _, err := librarypack.Write(context.Background(), pack, librarypack.Pack{CorpusGeneration: "test", MetadataGeneration: "test", Tracks: []librarypack.Track{{ID: "local-extra", Artist: "Local", Title: "Extra"}}}, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	args(t, "-dataset", "../../internal/evaluation/testdata/synthetic.json", "-catalog", "../../internal/catalog/testdata", "-paipack", pack, "-output", filepath.Join(dir, "report.json"), "-markdown", "", "-blind-output", filepath.Join(dir, "blind.json"), "-blind-key", filepath.Join(dir, "key.json"))
	if err := run(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "key.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "library_evidence_off") || !strings.Contains(string(raw), "library_evidence_on") {
		t.Fatal("blind defaults did not select pack variants")
	}
}

func TestRecommendationCLIReports(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "reports", "report.json")
	md := filepath.Join(dir, "reports", "report.md")
	args(t, "-dataset", "../../internal/evaluation/testdata/synthetic.json", "-catalog", "../../internal/catalog/testdata", "-output", out, "-markdown", md, "-blind-output", filepath.Join(dir, "blind.json"), "-blind-key", filepath.Join(dir, "key.json"))
	main()
	for _, path := range []string{out, md} {
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), "repository-structural-smoke") {
			t.Fatalf("missing report %s: %v", path, err)
		}
	}
	for _, name := range []string{"blind.json", "key.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecommendationCLIInputFailures(t *testing.T) {
	for name, values := range map[string][]string{"missing dataset": {}, "unknown flag": {"-bogus"}, "bad config": {"-dataset", "x", "-config", "absent"}, "bad catalog": {"-dataset", "x", "-catalog", "absent"}, "bad dataset": {"-dataset", "absent", "-catalog", "../../internal/catalog/testdata"}} {
		t.Run(name, func(t *testing.T) {
			args(t, values...)
			if run() == nil {
				t.Fatal("accepted invalid arguments")
			}
		})
	}
	if err := ensureParent("report.json"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureParent(filepath.Join(file, "nested", "report")); err == nil {
		t.Fatal("accepted file as parent")
	}
}

func TestDiscoveryCLIRejectsConflictingSourcesAndMissingInstallation(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		args(t, "-dataset", "unused", "-paipack", "library.paipack", "-discovery-state", "installed")
		if err := run(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("conflicting sources: %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing-state")
		args(t, "-dataset", "../../internal/evaluation/testdata/synthetic.json", "-catalog", "../../internal/catalog/testdata", "-discovery-state", path)
		if err := run(); err == nil || !strings.Contains(err.Error(), "activation record") {
			t.Fatalf("missing state: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("missing state directory was created")
		}
	})
}
