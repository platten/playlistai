package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/evaluation"
)

// App startup includes immutable pack verification and model hashing. It is
// outside the generation clock, but still bounded independently by the parent.
const desktopStartupAllowance = 5 * time.Minute

func superviseDesktopCases(ctx context.Context, options desktopEvaluationOptions, cases []promptCase) error {
	selected := desktopCases(cases, options.Split)
	if len(selected) == 0 {
		return fmt.Errorf("no cases in requested evaluation split")
	}
	if err := validateDesktopDiagnostics(options, len(selected)); err != nil {
		return err
	}
	if _, err := isolatedEvaluationDirectory(options.DataDir); err != nil {
		return err
	}
	caseDir := options.Output + ".cases"
	if err := os.MkdirAll(caseDir, 0700); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	report := desktopEvaluationReport{Version: 1, Started: time.Now().UTC(), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Capabilities: map[string]any{"supervised": true, "startupAllowanceSeconds": int64(desktopStartupAllowance / time.Second), "generationLimitSeconds": int64(options.CaseTimeout / time.Second)}, Runs: []desktopEvaluationRun{}, ListeningRuns: []evaluation.RelevanceRun{}}
	if err := writeDesktopReport(options.Output, report); err != nil {
		return err
	}
	for index, item := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		caseOutput := filepath.Join(caseDir, fmt.Sprintf("case-%03d.json", index+1))
		cancelPath := filepath.Join(caseDir, fmt.Sprintf("cancel-%03d", index+1))
		if err := os.Remove(cancelPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(caseOutput); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Printf("Supervising desktop %d/%d: %s\n", index+1, len(selected), item.Prompt)
		command := exec.Command(executable, desktopChildArguments(os.Args[1:], item.Prompt, caseOutput, cancelPath)...) //nolint:gosec // own executable and validated CLI arguments.
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		started := time.Now()
		timedOut, waitErr, startErr := runOwnedCommandWithCancel(ctx, command, options.CaseTimeout+desktopStartupAllowance, options.CleanupTimeout, func() { _ = os.WriteFile(cancelPath, []byte("cancel\n"), 0600) })
		child, readErr := readDesktopReport(caseOutput)
		row := desktopEvaluationRun{FamilyID: item.FamilyID, Prompt: item.Prompt, InputMode: "raw", GenerationLimitSeconds: int64(options.CaseTimeout / time.Second)}
		if options.Replay != "" {
			row.InputMode = "frozen"
		}
		listening := evaluation.RelevanceRun{FamilyID: item.FamilyID, Prompt: item.Prompt, Split: options.Split, Variant: options.Variant, InputMode: row.InputMode, CacheCondition: options.CacheCondition, Requested: item.Count, GenerationLimitMilliseconds: options.CaseTimeout.Milliseconds(), Tracks: []evaluation.BlindTrack{}}
		if readErr == nil && len(child.Runs) == 1 && child.Runs[0].FamilyID == item.FamilyID && child.Runs[0].Prompt == item.Prompt {
			row = child.Runs[0]
			if len(child.ListeningRuns) == 1 {
				listening = child.ListeningRuns[0]
			}
		} else {
			row.Milliseconds = time.Since(started).Milliseconds()
			row.Error = fmt.Sprintf("child report missing completed matching case: %v", readErr)
			populateDesktopFindings(&row, item, "")
		}
		if timedOut {
			row.TimedOut = true
			row.Error = "desktop child exceeded generation plus startup watchdog; owned process tree stopped"
		} else if startErr != nil {
			row.Error = "start desktop child: " + startErr.Error()
		} else if waitErr != nil && !desktopCaseFailed(row) {
			row.Error = "desktop child: " + waitErr.Error()
		}
		listening.Error = row.Error
		listening.Milliseconds = row.Milliseconds
		if readErr == nil {
			if report.Identities.CatalogVersion == "" {
				report.Identities, report.Executable = child.Identities, child.Executable
			}
			for key, value := range child.Capabilities {
				report.Capabilities[key] = value
			}
		}
		report.Runs = append(report.Runs, row)
		report.ListeningRuns = append(report.ListeningRuns, listening)
		if err := writeDesktopReport(options.Output, report); err != nil {
			return err
		}
		fmt.Printf("Desktop %d/%d: tracks=%d generation=%dms process=%s findings=%d error=%s\n", index+1, len(selected), len(row.Result.Playlist.Tracks), row.Milliseconds, time.Since(started).Round(time.Second), len(row.ParserFindings)+len(row.InterpretationFindings)+len(row.ConstraintFindings), row.Error)
	}
	report.Completed = true
	if err := writeDesktopReport(options.Output, report); err != nil {
		return err
	}
	for _, row := range report.Runs {
		if desktopCaseFailed(row) {
			return fmt.Errorf("one or more desktop operations or assertions failed; see %s", options.Output)
		}
	}
	return nil
}

func readDesktopReport(path string) (desktopEvaluationReport, error) {
	var report desktopEvaluationReport
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &report)
	}
	return report, err
}

func desktopChildArguments(base []string, prompt, output, cancelPath string) []string {
	var args []string
	for index := 0; index < len(base); index++ {
		arg := base[index]
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "case" || name == "output" || name == "cancel-file" || name == "supervised-child" {
			if name != "supervised-child" && !inline && index+1 < len(base) {
				index++
			}
			continue
		}
		args = append(args, arg)
	}
	return append(args, "-supervised-child", "-case", prompt, "-output", output, "-cancel-file", cancelPath)
}
