package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestDesktopEvaluationRejectsNormalAndLinkedWritableStores(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	root := t.TempDir()
	if _, err := isolatedEvaluationDirectory(root); err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedEvaluationDirectory(config.Default().DataDir); err == nil {
		t.Fatal("normal app store accepted")
	}
	linked := filepath.Join(root, "prefs.json")
	target := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := isolatedEvaluationDirectory(root); err == nil {
		t.Fatal("linked writable preferences accepted")
	}
}

func TestDesktopEvaluationRejectsStandaloneProviderFlags(t *testing.T) {
	commandArgs(t, "-app-data-dir", t.TempDir(), "-online", "-prompts", "../../internal/evaluation/testdata/varied-prompts-v1.json")
	if err := run(); err == nil {
		t.Fatal("standalone provider flags silently ignored")
	}
}

func TestDesktopSingleCaseStillUsesOuterSupervisor(t *testing.T) {
	root := t.TempDir()
	prompt := "Classical, 10 tracks."
	output := filepath.Join(root, "report.json")
	commandArgs(t, "-app-data-dir", root, "-prompts", "../../internal/evaluation/testdata/varied-prompts-v1.json", "-case", prompt, "-output", output)
	// The child is this test executable, whose test flags reject CLI arguments.
	// No app, model, or provider starts; the parent must record its failed child.
	if err := run(); err == nil {
		t.Fatal("expected test child CLI failure")
	}
	report, err := readDesktopReport(output)
	if err != nil {
		t.Fatalf("single case bypassed outer supervisor: %v", err)
	}
	if report.Capabilities["supervised"] != true || !report.Completed || len(report.Runs) != 1 || report.Runs[0].Prompt != prompt || report.Runs[0].Error == "" {
		t.Fatalf("failed single child was not bounded and recorded: %+v", report)
	}
}

func TestDesktopReadinessWaitsForRequestedModel(t *testing.T) {
	if desktopReady(false, ports.ParserInfo{Backend: "rules", Ready: true}, "llama") {
		t.Fatal("ready rules fallback bypassed requested model startup")
	}
	if desktopReady(true, ports.ParserInfo{Backend: "llama", Ready: true}, "llama") {
		t.Fatal("audio startup still pending")
	}
	if !desktopReady(false, ports.ParserInfo{Backend: "llama", Ready: true}, "llama") || !desktopReady(false, ports.ParserInfo{Backend: "rules", Ready: true}, "rules") {
		t.Fatal("requested ready parser rejected")
	}
}

func TestDesktopRejectsExplicitIgnoredFlagsIncludingFalse(t *testing.T) {
	for _, name := range []string{"online", "min-tracks", "context-size", "cache", "cached-audio-only"} {
		t.Run(name, func(t *testing.T) {
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.String(name, "", "")
			if err := flags.Parse([]string{"--" + name + "=false"}); err != nil {
				t.Fatal(err)
			}
			if validateDesktopFlags(flags) == nil {
				t.Fatal("explicit unused option accepted")
			}
		})
	}
}

func TestDesktopAllowsExplicitAutomaticMode(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.String("mode", "", "")
	if err := flags.Parse([]string{"-mode=automatic"}); err != nil {
		t.Fatal(err)
	}
	if err := validateDesktopFlags(flags); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopFindingsKeepPartialCompletenessSeparate(t *testing.T) {
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{TotalTrackCount: 10}}
	row := desktopEvaluationRun{InputMode: "raw", Result: bridge.GenerateResult{Status: bridge.GenerationStatus{Parser: bridge.ParserStatus{Backend: "llama", RequestedBackend: "llama"}}, Playlist: bridge.PlaylistResult{Intent: intent, Tracks: []bridge.PlaylistTrack{{ID: "one", Artist: "Radiohead", Title: "One"}, {ID: "two", Artist: "Radiohead", Title: "Two"}}}}}
	populateDesktopFindings(&row, promptCase{Count: 10, OnlyArtist: "Radiohead"}, "llama")
	if desktopCaseFailed(row) || row.Completeness.ExactCount || row.Completeness.Returned != 2 || row.Completeness.Requested != 10 || row.Parser == nil {
		t.Fatalf("honest short result conflated with deterministic failure: %+v", row)
	}
	row.Result.Playlist.Tracks[1] = bridge.PlaylistTrack{ID: "other", Artist: "Other Artist", Title: "Other"}
	row.Result.Status.Parser.FallbackUsed, row.Result.Status.Parser.Backend = true, "rules"
	populateDesktopFindings(&row, promptCase{Count: 10, OnlyArtist: "Radiohead"}, "llama")
	if len(row.ConstraintFindings) == 0 || len(row.ParserFindings) == 0 || !desktopCaseFailed(row) {
		t.Fatalf("constraint or fallback lost: %+v", row)
	}
}

func TestDesktopDoesNotInferActualParserWhenStatusMissing(t *testing.T) {
	row := desktopEvaluationRun{InputMode: "raw", Error: "parse failed"}
	populateDesktopFindings(&row, promptCase{Count: 10}, "llama")
	if row.Parser != nil || len(row.ParserFindings) == 0 {
		t.Fatalf("invented parser evidence: %+v", row)
	}
}

func TestDesktopChildArgumentsPreserveModelAndBudget(t *testing.T) {
	got := desktopChildArguments([]string{"-model", "/model.gguf", "--case=old", "-output", "old.json", "-case-timeout", "5m", "-runtime", "/llama", "-supervised-child"}, "new", "new.json", "cancel")
	want := []string{"-model", "/model.gguf", "-case-timeout", "5m", "-runtime", "/llama", "-supervised-child", "-case", "new", "-output", "new.json", "-cancel-file", "cancel"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments=%q, want %q", got, want)
	}
}

func TestDesktopReportAtomicPermissionsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	want := desktopEvaluationReport{Version: 1, Runs: []desktopEvaluationRun{{Prompt: "public", GenerationLimitSeconds: 300}}}
	if err := writeDesktopReport(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readDesktopReport(path)
	if err != nil || got.Version != 1 || len(got.Runs) != 1 || got.Runs[0].Prompt != "public" || got.Runs[0].GenerationLimitSeconds != 300 {
		t.Fatalf("read=%+v error=%v", got, err)
	}
	for _, file := range []string{path, path + ".listening.json"} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("report permissions=%v", info.Mode())
		}
	}
}
