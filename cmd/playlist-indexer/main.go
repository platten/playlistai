// playlist-indexer is the headless, resumable local-library analyzer.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/libraryindex"
)

var resolvedShutdownNanos atomic.Int64

type gracefulStopContextKey struct{}

type commandExecutor func(context.Context, []string, io.Writer, io.Writer) (int, error)

func withGracefulStop(ctx context.Context, stop <-chan struct{}) context.Context {
	return context.WithValue(ctx, gracefulStopContextKey{}, stop)
}

func gracefulStopFromContext(ctx context.Context) <-chan struct{} {
	stop, _ := ctx.Value(gracefulStopContextKey{}).(<-chan struct{})
	return stop
}

func gracefulStopRequested(ctx context.Context) bool {
	stop := gracefulStopFromContext(ctx)
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	if len(os.Args) == 3 && os.Args[1] == "--mert-worker" {
		if err := audioruntime.RunMERT(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	resolvedShutdownNanos.Store(0)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return runCoordinated(os.Args[1:], os.Stdout, os.Stderr, signals, execute)
}

func runCoordinated(args []string, stdout, stderr io.Writer, signals <-chan os.Signal, executor commandExecutor) int {
	workCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopAdmission := make(chan struct{})
	ctx := withGracefulStop(workCtx, stopAdmission)
	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := executor(ctx, args, stdout, stderr)
		done <- answer{code: code, err: err}
	}()
	var result answer
	interrupted := false
	select {
	case result = <-done:
	case <-signals:
		interrupted = true
		close(stopAdmission)
		fmt.Fprintln(stderr, "interrupt received: stopping new work and draining current operations")
		timeout := time.Duration(resolvedShutdownNanos.Load())
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case result = <-done:
		case <-signals:
			fmt.Fprintln(stderr, "second signal: forcing immediate shutdown; pending jobs remain recoverable")
			cancel()
			return 130
		case <-timer.C:
			fmt.Fprintln(stderr, "shutdown deadline exceeded: canceling remaining owned work; pending jobs remain recoverable")
			cancel()
			forceTimer := time.NewTimer(5 * time.Second)
			defer forceTimer.Stop()
			select {
			case result = <-done:
			case <-signals:
				fmt.Fprintln(stderr, "second signal: forcing immediate shutdown; pending jobs remain recoverable")
				return 130
			case <-forceTimer.C:
				fmt.Fprintln(stderr, "owned work did not stop after cancellation; exiting with durable leases recoverable on restart")
				return 130
			}
		}
	}
	code, err := result.code, result.err
	interruptionErr := errors.Is(err, libraryindex.ErrShutdownRequested) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if err != nil && !interruptionErr {
		fmt.Fprintln(stderr, err)
	}
	if interrupted || interruptionErr {
		if interrupted {
			if err == nil || interruptionErr {
				fmt.Fprintln(stderr, "graceful shutdown complete: durable state is closed and unfinished work can resume on the next invocation")
			} else {
				fmt.Fprintln(stderr, "shutdown finished with a cleanup error; run doctor before resuming")
			}
		}
		code = 130
	}
	return code
}
