package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestSupervisorProcessHelper(t *testing.T) {
	mode := os.Getenv("PLAYLISTAI_MUSICCHECK_SUPERVISOR_HELPER")
	if mode == "" {
		return
	}
	if mode == "hang" {
		signal.Ignore(os.Interrupt)
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestRunOwnedCommandCompletionAndTimeout(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("completion", func(t *testing.T) {
		cmd := exec.Command(executable, "-test.run=^TestSupervisorProcessHelper$")
		cmd.Env = append(os.Environ(), "PLAYLISTAI_MUSICCHECK_SUPERVISOR_HELPER=complete")
		timedOut, waitErr, startErr := runOwnedCommand(context.Background(), cmd, 5*time.Second, time.Second)
		if startErr != nil || waitErr != nil || timedOut {
			t.Fatalf("completion timedOut=%v wait=%v start=%v", timedOut, waitErr, startErr)
		}
	})
	t.Run("timeout kills and reaps", func(t *testing.T) {
		cmd := exec.Command(executable, "-test.run=^TestSupervisorProcessHelper$")
		cmd.Env = append(os.Environ(), "PLAYLISTAI_MUSICCHECK_SUPERVISOR_HELPER=hang")
		started := time.Now()
		timedOut, _, startErr := runOwnedCommand(context.Background(), cmd, 25*time.Millisecond, 25*time.Millisecond)
		if startErr != nil || !timedOut || cmd.ProcessState == nil {
			t.Fatalf("timeout timedOut=%v state=%v start=%v", timedOut, cmd.ProcessState, startErr)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("bounded cleanup took %s", elapsed)
		}
	})
}

func TestSupervisedArgumentsAndPartialReportPreservation(t *testing.T) {
	got := supervisedArguments([]string{"-model", "model.gguf", "-runtime=llama", "-output", "old.json", "-online", "-case=old", "-min-tracks", "5"}, "new prompt", "new.json", "cancel", "http://127.0.0.1:1")
	want := []string{"-online", "-min-tracks", "5", "-supervised-child", "-case", "new prompt", "-output", "new.json", "-cancel-file", "cancel", "-server-url", "http://127.0.0.1:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments=%q want %q", got, want)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "case.json")
	if err := writeResults(path, []result{{Prompt: "new prompt", Completed: true, CompletionState: "failed", Errors: []string{"assertion"}}}); err != nil {
		t.Fatal(err)
	}
	loaded := readCaseResult(path, "new prompt")
	if !loaded.Completed || loaded.CompletionState != "failed" || len(loaded.Errors) != 1 {
		t.Fatalf("partial report lost: %+v", loaded)
	}
	missing := readCaseResult(filepath.Join(dir, "missing.json"), "next")
	if missing.Completed || missing.CompletionState != "failed" || len(missing.Errors) == 0 {
		t.Fatalf("missing report not synthesized: %+v", missing)
	}
}

func TestGracefulCaseBudgetIncludesParsingAndPreparation(t *testing.T) {
	if got := gracefulCaseBudget(120*time.Second, 10*time.Second, 30*time.Second); got != 75*time.Second {
		t.Fatalf("budget=%s", got)
	}
	if got := gracefulCaseBudgetForIntent(120*time.Second, 10*time.Second, 30*time.Second, core.MusicIntent{Count: 10, HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals"}}}); got != 85*time.Second {
		t.Fatalf("vocal-screened budget=%s", got)
	}
	if got := gracefulCaseBudgetForIntent(120*time.Second, 10*time.Second, 30*time.Second, core.MusicIntent{Count: 20, HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals"}}}); got != 65*time.Second {
		t.Fatalf("twenty-track vocal-screened budget=%s", got)
	}
	if got := gracefulCaseBudget(20*time.Second, 10*time.Second, 15*time.Second); got < time.Second || got > 2*time.Second {
		t.Fatalf("minimum budget=%s", got)
	}
}
