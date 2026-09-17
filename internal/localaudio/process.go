package localaudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
	"time"
)

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int64
	total  int64
	over   bool
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	remaining := w.limit - int64(w.buffer.Len())
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:min(int64(len(p)), remaining)])
	}
	if w.total > w.limit {
		w.over = true
		return 0, ErrOutputLimit
	}
	return len(p), nil
}

func (w *boundedBuffer) Bytes() []byte  { return w.buffer.Bytes() }
func (w *boundedBuffer) String() string { return w.buffer.String() }

func runBounded(ctx context.Context, timeout time.Duration, executable string, args []string, stdoutLimit, stderrLimit int64) ([]byte, string, error) {
	if timeout <= 0 {
		return nil, "", fmt.Errorf("localaudio: subprocess timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...) //nolint:gosec // executable is checksum-verified and args are never interpreted by a shell.
	cmd.WaitDelay = 5 * time.Second
	isolateProcess(cmd)
	stdout := &boundedBuffer{limit: stdoutLimit}
	stderr := &boundedBuffer{limit: stderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.over || stderr.over {
		return nil, stderr.String(), ErrOutputLimit
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, stderr.String(), ctxErr
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, stderr.String(), fmt.Errorf("localaudio: subprocess exited with status %d: %s", exit.ExitCode(), boundedDiagnostic(stderr.String()))
		}
		return nil, stderr.String(), err
	}
	return append([]byte(nil), stdout.Bytes()...), stderr.String(), nil
}

type activityWriter struct {
	activity    chan<- struct{}
	destination io.Writer
}

func (w activityWriter) Write(p []byte) (int, error) {
	select {
	case w.activity <- struct{}{}:
	default:
	}
	return w.destination.Write(p)
}

// runDiscarding bounds diagnostics and process lifetime while streaming stdout
// directly to a sink. Integrity validation can decode very large files without
// retaining their PCM output in memory.
func runDiscarding(ctx context.Context, timeout, stallTimeout time.Duration, executable string, args []string, stderrLimit int64) (string, error) {
	return runStreaming(ctx, timeout, stallTimeout, executable, args, io.Discard, stderrLimit)
}

// runStreaming bounds diagnostics and process lifetime while sending stdout to
// destination. The destination must consume writes promptly; child-process
// backpressure is intentional and remains cancelable through the owned process.
func runStreaming(ctx context.Context, timeout, stallTimeout time.Duration, executable string, args []string, destination io.Writer, stderrLimit int64) (string, error) {
	if timeout <= 0 {
		return "", fmt.Errorf("localaudio: subprocess timeout must be positive")
	}
	if destination == nil {
		return "", fmt.Errorf("localaudio: subprocess destination is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...) //nolint:gosec // executable is checksum-verified and args are never interpreted by a shell.
	cmd.WaitDelay = 5 * time.Second
	isolateProcess(cmd)
	stderr := &boundedBuffer{limit: stderrLimit}
	activity := make(chan struct{}, 1)
	if stallTimeout > 0 {
		cmd.Stdout = activityWriter{activity: activity, destination: destination}
	} else {
		cmd.Stdout = destination
	}
	cmd.Stderr = stderr
	watchDone := make(chan struct{})
	watchStop := make(chan struct{})
	var stalled, completed atomic.Bool
	if stallTimeout > 0 {
		go func() {
			defer close(watchDone)
			timer := time.NewTimer(stallTimeout)
			defer timer.Stop()
			for {
				select {
				case <-activity:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(stallTimeout)
				case <-timer.C:
					if completed.Load() {
						return
					}
					stalled.Store(true)
					cancel()
					return
				case <-watchStop:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	} else {
		close(watchDone)
	}
	err := cmd.Run()
	completed.Store(true)
	close(watchStop)
	<-watchDone
	if stderr.over {
		return stderr.String(), ErrOutputLimit
	}
	if stalled.Load() {
		return stderr.String(), fmt.Errorf("%w for %s", ErrProcessStalled, stallTimeout)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stderr.String(), ctxErr
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stderr.String(), fmt.Errorf("localaudio: subprocess exited with status %d: %s", exit.ExitCode(), boundedDiagnostic(stderr.String()))
		}
		return stderr.String(), err
	}
	return stderr.String(), nil
}

func boundedDiagnostic(value string) string {
	value = string(bytes.ToValidUTF8([]byte(value), []byte("?")))
	if value == "" {
		return "no diagnostic output"
	}
	return value
}
