package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/logging"
)

func TestDesktopDiagnosticsSingleCaseAndOutputValidation(t *testing.T) {
	root := t.TempDir()
	options := desktopEvaluationOptions{DataDir: filepath.Join(root, "store"), Output: filepath.Join(root, "report.json"), Diagnostics: filepath.Join(root, "diagnostics.json")}
	if err := validateDesktopDiagnostics(options, 1); err != nil {
		t.Fatal(err)
	}
	if err := validateDesktopDiagnostics(options, 2); err == nil {
		t.Fatal("multiple diagnostic cases accepted")
	}
	for _, path := range []string{options.Output, options.Output + ".listening.json", filepath.Join(options.Output+".cases", "case-001.json"), filepath.Join(root, "sub", "..", "report.json")} {
		options.Diagnostics = path
		if err := validateDesktopDiagnostics(options, 1); err == nil {
			t.Fatalf("colliding diagnostic path accepted: %s", path)
		}
	}
	options.Diagnostics = filepath.Join(root, "existing.json")
	if err := os.WriteFile(options.Diagnostics, []byte("preserve"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateDesktopDiagnostics(options, 1); err == nil {
		t.Fatal("existing diagnostic output accepted")
	}
	if raw, err := os.ReadFile(options.Diagnostics); err != nil || string(raw) != "preserve" {
		t.Fatal("existing diagnostic output changed")
	}
	options.Diagnostics = ""
	if err := validateDesktopDiagnostics(options, 2); err != nil {
		t.Fatalf("ordinary multi-case evaluation changed: %v", err)
	}
}

func TestDesktopDiagnosticsRejectsLinkedParentCollision(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	options := desktopEvaluationOptions{DataDir: filepath.Join(root, "store"), Output: filepath.Join(root, "report.json"), Diagnostics: filepath.Join(alias, "report.json")}
	if err := validateDesktopDiagnostics(options, 1); err == nil {
		t.Fatal("symlink-parent output collision accepted")
	}
	options.Diagnostics = filepath.Join(alias, "report.json.cases", "case-001.json")
	if err := validateDesktopDiagnostics(options, 1); err == nil {
		t.Fatal("symlink-parent nested case collision accepted")
	}
	options.DataDir = root
	options.Diagnostics = filepath.Join(alias, "evaluation.log")
	if err := validateDesktopDiagnostics(options, 1); err == nil {
		t.Fatal("symlink-parent store collision accepted")
	}
}

func TestDesktopDiagnosticsRejectsStoreArtifactsBeforeMutation(t *testing.T) {
	for _, name := range []string{"evaluation.log", "evaluation-metadata.sqlite"} {
		for entrypoint, runEvaluation := range map[string]func(context.Context, desktopEvaluationOptions, []promptCase) error{
			"supervisor": superviseDesktopCases,
			"child":      runDesktopEvaluation,
		} {
			t.Run(name+"/"+entrypoint, func(t *testing.T) {
				root := t.TempDir()
				options := desktopEvaluationOptions{DataDir: filepath.Join(root, "store"), Output: filepath.Join(root, "report.json"), Split: "development", Variant: "fixture", CacheCondition: "unknown"}
				options.Diagnostics = filepath.Join(options.DataDir, name)
				err := runEvaluation(context.Background(), options, []promptCase{{Prompt: "fixture"}})
				if err == nil || !strings.Contains(err.Error(), "outside the isolated app-data-dir") {
					t.Fatalf("store collision not rejected: %v", err)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("validation mutated evaluation artifacts: entries=%v error=%v", entries, err)
				}
			})
		}
	}
}

func TestDesktopDiagnosticsCLIRejectsMultipleCasesBeforeStarting(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "report.json")
	commandArgs(t, "-app-data-dir", filepath.Join(root, "store"), "-prompts", "../../internal/evaluation/testdata/varied-prompts-v1.json", "-output", output, "-diagnostics", filepath.Join(root, "diagnostics.json"))
	if err := run(); err == nil || !strings.Contains(err.Error(), "exactly one selected case") {
		t.Fatalf("wrong validation result: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("report written before validation: %v", err)
	}
}

func TestDesktopDiagnosticsAllowedAndForwardedToChild(t *testing.T) {
	for _, base := range [][]string{{"-diagnostics", "/private/trace.json"}, {"--diagnostics=/private/trace.json"}} {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		flags.String("diagnostics", "", "")
		if err := flags.Parse(base); err != nil {
			t.Fatal(err)
		}
		if err := validateDesktopFlags(flags); err != nil {
			t.Fatal(err)
		}
		got := desktopChildArguments(base, "prompt", "child.json", "cancel")
		want := append(append([]string{}, base...), "-supervised-child", "-case", "prompt", "-output", "child.json", "-cancel-file", "cancel")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("diagnostic opt-in not forwarded: got=%q want=%q", got, want)
		}
	}
}

func TestDesktopDiagnosticsExportsPrivateFileOnEarlyError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "diagnostics.json")
	options := desktopEvaluationOptions{DataDir: filepath.Join(root, "store"), Config: filepath.Join(root, "invalid.toml"), Output: filepath.Join(root, "report.json"), Diagnostics: path, Split: "development", Variant: "fixture", CacheCondition: "unknown"}
	if err := os.WriteFile(options.Config, []byte("[invalid TOML"), 0600); err != nil {
		t.Fatal(err)
	}
	_, configErr := config.Load(options.Config)
	if configErr == nil {
		t.Fatal("invalid config fixture accepted")
	}
	err := runDesktopEvaluation(context.Background(), options, []promptCase{{Prompt: "fixture"}})
	if err == nil {
		t.Fatal("invalid config unexpectedly succeeded")
	}
	// Configuration fails before app/model/provider startup. The deferred export
	// must still execute and preserve the original failure.
	if err.Error() != configErr.Error() {
		t.Fatalf("original error changed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []logging.Entry
	if err = json.Unmarshal(raw, &entries); err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("invalid empty diagnostic export: %q %v", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("sensitive output is not private: info=%v error=%v", info, err)
	}
	options.Diagnostics = ""
	if err := runDesktopEvaluation(context.Background(), options, []promptCase{{Prompt: "fixture"}}); err == nil || err.Error() != configErr.Error() {
		t.Fatalf("opt-out changed config failure: %v", err)
	}
}
