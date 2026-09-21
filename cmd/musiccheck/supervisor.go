package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/llama"
	processutil "github.com/platten/playlistai/internal/process"
)

type supervisorConfig struct {
	Output         string
	Model          string
	Runtime        string
	ServerURL      string
	ContextSize    int
	CaseTimeout    time.Duration
	CleanupTimeout time.Duration
	Identities     runIdentities
}

// Child initialization opens immutable catalog/model assets before the
// per-prompt timer and stage accounting begin. Bound that isolated startup
// separately; every completed report is still required to stay within the
// configured prompt timeout below.
const supervisedStartupAllowance = 30 * time.Second

func gracefulCaseBudget(limit, cleanup, elapsed time.Duration) time.Duration {
	// The supervisor grants the full cleanup window after the hard deadline.
	// Ordinary final assembly has a smaller observed cost than vocal-screened
	// assembly, which receives its own reserve below.
	reportReserve := min(15*time.Second, limit/6)
	remaining := limit - reportReserve - elapsed
	if remaining < time.Second {
		return time.Second
	}
	return remaining
}

func gracefulCaseBudgetForIntent(limit, cleanup, elapsed time.Duration, intent core.MusicIntent) time.Duration {
	if !core.WantsInstrumental(intent) {
		return gracefulCaseBudget(limit, cleanup, elapsed)
	}
	// Vocal-screened final validation grows with the requested list size.
	// Ten-track runs need their discovery time; twenty-track runs need a wider
	// assembly reserve. Neither changes the supervisor's hard deadline.
	reserve := 5*time.Second + time.Duration(max(0, intent.Count-10))*2*time.Second
	remaining := limit - min(reserve, limit/4) - elapsed
	if remaining < time.Second {
		return time.Second
	}
	return remaining
}

func superviseCases(ctx context.Context, cases []promptCase, cfg supervisorConfig) error {
	serverURL := cfg.ServerURL
	var managed *llama.Parser
	if !argumentPresent(os.Args[1:], "rules-parser") && !argumentPresent(os.Args[1:], "replay") {
		if serverURL == "" {
			var err error
			managed, err = llama.New(ctx, llama.Options{
				BinaryPath: cfg.Runtime, ModelPath: cfg.Model, NCtx: cfg.ContextSize,
				GPULayers: 0, StartTimeout: 3 * time.Minute,
			})
			if err != nil {
				return err
			}
			defer managed.Close()
			serverURL = managed.ServerURL()
		}
		client := llama.NewClientWithContext(serverURL, cfg.ContextSize)
		if !client.Healthy(ctx) {
			return fmt.Errorf("local llama server is not healthy")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "playlist-ai-musiccheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	results := make([]result, 0, len(cases))
	failed := false
	for index, c := range cases {
		if err := ctx.Err(); err != nil {
			return err
		}
		caseOutput := filepath.Join(temporary, fmt.Sprintf("case-%03d.json", index+1))
		cancelPath := filepath.Join(temporary, fmt.Sprintf("cancel-%03d", index+1))
		started := time.Now()
		args := supervisedArguments(os.Args[1:], c.Prompt, caseOutput, cancelPath, serverURL)
		fmt.Printf("Supervising %d/%d: %s\n", index+1, len(cases), c.Prompt)
		cmd := exec.Command(executable, args...) //nolint:gosec // current executable and validated local arguments.
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		timedOut, waitErr, startErr := runOwnedCommandWithCancel(ctx, cmd, cfg.CaseTimeout+supervisedStartupAllowance, cfg.CleanupTimeout, func() {
			_ = os.WriteFile(cancelPath, []byte("cancel\n"), 0600)
		})
		if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
			return waitErr
		}
		r := readCaseResult(caseOutput, c.Prompt)
		catalogVersion := r.Identities.CatalogVersion
		r.Identities = cfg.Identities
		r.Identities.CatalogVersion = catalogVersion
		if startErr != nil {
			r.Errors = append(r.Errors, "start supervised case: "+startErr.Error())
		}
		if timedOut {
			r.Completed = false
			r.TimedOut = true
			r.CompletionState = "timed_out"
			r.Milliseconds = time.Since(started).Milliseconds()
			r.Errors = appendUnique(r.Errors, fmt.Sprintf("case exceeded %s timeout", cfg.CaseTimeout))
		} else {
			if r.Completed && r.Milliseconds > cfg.CaseTimeout.Milliseconds() {
				r.Completed = false
				r.TimedOut = true
				r.CompletionState = "timed_out"
				r.Errors = appendUnique(r.Errors, fmt.Sprintf("case exceeded %s prompt timeout", cfg.CaseTimeout))
			}
			if !r.Completed && !r.TimedOut {
				r.CompletionState = "failed"
			}
			if waitErr != nil && len(r.Errors) == 0 {
				r.Errors = append(r.Errors, "supervised case process: "+waitErr.Error())
				r.CompletionState = "failed"
			}
		}
		failed = failed || !r.Completed || r.TimedOut || len(r.Errors) > 0
		results = append(results, r)
		if err := writeResults(cfg.Output, results); err != nil {
			return err
		}
	}
	if failed {
		return fmt.Errorf("one or more supervised prompt checks failed; see %s", cfg.Output)
	}
	return nil
}

func runOwnedCommand(ctx context.Context, cmd *exec.Cmd, timeout, cleanup time.Duration) (timedOut bool, waitErr, startErr error) {
	return runOwnedCommandWithCancel(ctx, cmd, timeout, cleanup, nil)
}

func runOwnedCommandWithCancel(ctx context.Context, cmd *exec.Cmd, timeout, cleanup time.Duration, requestCancel func()) (timedOut bool, waitErr, startErr error) {
	processutil.Owned(cmd)
	if startErr = cmd.Start(); startErr != nil {
		return false, nil, startErr
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case waitErr = <-done:
		return false, waitErr, nil
	case <-timer.C:
		timedOut = true
	case <-ctx.Done():
		if requestCancel != nil {
			requestCancel()
		}
		_ = processutil.InterruptOwned(cmd)
		_ = waitForOwnedExit(cmd, done, cleanup)
		return false, ctx.Err(), nil
	}
	if requestCancel != nil {
		requestCancel()
	}
	_ = processutil.InterruptOwned(cmd)
	waitErr = waitForOwnedExit(cmd, done, cleanup)
	return timedOut, waitErr, nil
}

func waitForOwnedExit(cmd *exec.Cmd, done <-chan error, cleanup time.Duration) error {
	grace := time.NewTimer(cleanup)
	defer grace.Stop()
	select {
	case err := <-done:
		return err
	case <-grace.C:
		_ = processutil.KillOwned(cmd)
		return <-done
	}
}

func readCaseResult(path, prompt string) result {
	r := result{Prompt: prompt, Errors: []string{}, CompletionState: "failed"}
	raw, err := os.ReadFile(path)
	if err != nil {
		r.Errors = append(r.Errors, "case report unavailable: "+err.Error())
		return r
	}
	var rows []result
	if err = json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 {
		if err == nil {
			err = fmt.Errorf("expected one record, got %d", len(rows))
		}
		r.Errors = append(r.Errors, "invalid case report: "+err.Error())
		return r
	}
	return rows[0]
}

func writeResults(path string, results []result) error {
	report, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(report, '\n'), 0600)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func argumentPresent(args []string, name string) bool {
	prefix := "-" + name
	for _, arg := range args {
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

func supervisedArguments(base []string, prompt, output, cancelPath, serverURL string) []string {
	removed := map[string]bool{
		"case": true, "output": true, "model": true, "runtime": true,
		"server-url": true, "cancel-file": true, "supervised-child": false,
	}
	args := make([]string, 0, len(base)+7)
	for i := 0; i < len(base); i++ {
		arg := base[i]
		name := strings.TrimPrefix(arg, "-")
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		takesValue, remove := removed[name]
		if !remove && name != "supervised-child" {
			args = append(args, arg)
			continue
		}
		if remove && takesValue && !strings.Contains(arg, "=") && i+1 < len(base) {
			i++
		}
	}
	args = append(args, "-supervised-child", "-case", prompt, "-output", output, "-cancel-file", cancelPath)
	if serverURL != "" {
		args = append(args, "-server-url", serverURL)
	}
	return args
}

func watchCancellationFile(ctx context.Context, path string, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(path); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
