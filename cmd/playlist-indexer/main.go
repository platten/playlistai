// playlist-indexer is the headless, resumable local-library analyzer.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/platten/playlistai/internal/audioruntime"
)

var resolvedShutdownNanos atomic.Int64

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
		done <- answer{code: code, err: err}
	}()
	var result answer
	select {
	case result = <-done:
	case <-signals:
		cancel() // stop discovery/claims and cancel owned native work
		timeout := time.Duration(resolvedShutdownNanos.Load())
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case result = <-done:
		case <-signals:
			fmt.Fprintln(os.Stderr, "second signal: forcing immediate shutdown; pending jobs remain recoverable")
			return 130
		case <-timer.C:
			fmt.Fprintln(os.Stderr, "shutdown deadline exceeded; pending jobs remain recoverable")
			return 130
		}
	}
	code, err := result.code, result.err
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = 130
	}
	return code
}
