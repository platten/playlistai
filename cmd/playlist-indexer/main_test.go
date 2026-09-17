package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestFirstInterruptStopsAdmissionAndWaitsForCurrentOperation(t *testing.T) {
	signals := make(chan os.Signal, 2)
	started := make(chan struct{})
	stopSeen := make(chan struct{})
	finish := make(chan struct{})
	executor := func(ctx context.Context, _ []string, _, _ io.Writer) (int, error) {
		close(started)
		stop := gracefulStopFromContext(ctx)
		if stop == nil {
			return 1, errors.New("missing graceful stop channel")
		}
		<-stop
		if ctx.Err() != nil {
			return 1, errors.New("first interrupt canceled current work")
		}
		close(stopSeen)
		<-finish
		return 130, libraryindex.ErrShutdownRequested
	}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runCoordinated(nil, &stdout, &stderr, signals, commandExecutor(executor))
	}()
	<-started
	signals <- os.Interrupt
	<-stopSeen
	select {
	case code := <-done:
		t.Fatalf("shutdown returned before current work drained: %d", code)
	default:
	}
	close(finish)
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("exit code=%d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful shutdown did not finish")
	}
	log := stderr.String()
	if !strings.Contains(log, "stopping new work") || !strings.Contains(log, "unfinished work can resume") {
		t.Fatalf("shutdown diagnostics=%q", log)
	}
}

func TestShutdownTimeoutCancelsOwnedWork(t *testing.T) {
	previous := resolvedShutdownNanos.Load()
	resolvedShutdownNanos.Store(int64(20 * time.Millisecond))
	defer resolvedShutdownNanos.Store(previous)
	signals := make(chan os.Signal, 2)
	started := make(chan struct{})
	executor := func(ctx context.Context, _ []string, _, _ io.Writer) (int, error) {
		close(started)
		<-ctx.Done()
		return 130, ctx.Err()
	}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runCoordinated(nil, &stdout, &stderr, signals, commandExecutor(executor))
	}()
	<-started
	signals <- os.Interrupt
	select {
	case code := <-done:
		if code != 130 || !strings.Contains(stderr.String(), "shutdown deadline exceeded") {
			t.Fatalf("code=%d diagnostics=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout did not cancel owned work")
	}
}
