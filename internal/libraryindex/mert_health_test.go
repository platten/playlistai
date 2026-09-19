package libraryindex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mertHealthEvents struct {
	mu     sync.Mutex
	events []bool
}

func (e *mertHealthEvents) record(healthy bool, _ error) {
	e.mu.Lock()
	e.events = append(e.events, healthy)
	e.mu.Unlock()
}

func (e *mertHealthEvents) snapshot() []bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]bool(nil), e.events...)
}

func newTestMERTSupervisor(t *testing.T, probe mertProbe, events *mertHealthEvents) *mertSupervisor {
	t.Helper()
	s := newMERTSupervisor(context.Background(), probe, events.record)
	s.liveness, s.recoveryInitial, s.recoveryMaximum = 20*time.Millisecond, 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(s.stop)
	return s
}

func TestMERTSupervisorPausesAnalysisUntilHealthCheckPasses(t *testing.T) {
	var recoveryProbes atomic.Int32
	events := &mertHealthEvents{}
	s := newTestMERTSupervisor(t, func(context.Context, bool) (bool, error) {
		if recoveryProbes.Add(1) < 3 {
			return true, errors.New("cuda device unavailable")
		}
		return true, nil
	}, events)
	if !s.confirmOutage(context.Background(), func(context.Context) error { return errors.New("restart failed health") }) {
		t.Fatal("failed health check did not declare an outage")
	}
	if s.healthy() {
		t.Fatal("analysis was not paused")
	}
	// A second worker failing during the same outage must not start another
	// recovery loop or count another outage.
	s.confirmOutage(context.Background(), func(context.Context) error { return errors.New("still down") })

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.wait(waitCtx, nil); err != nil {
		t.Fatalf("analysis did not resume after recovery: %v", err)
	}
	if got := recoveryProbes.Load(); got != 3 {
		t.Fatalf("recovery probes=%d, want retries until the third check passed", got)
	}
	if got := s.outages.Load(); got != 1 {
		t.Fatalf("outages=%d, want 1", got)
	}
	if got := events.snapshot(); len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("health events=%v, want [false true]", got)
	}
}

func TestMERTSupervisorLeavesTrackFailureWithTrackWhenRestartIsHealthy(t *testing.T) {
	events := &mertHealthEvents{}
	s := newTestMERTSupervisor(t, func(context.Context, bool) (bool, error) { return true, nil }, events)
	if s.confirmOutage(context.Background(), func(context.Context) error { return nil }) {
		t.Fatal("healthy restart was treated as an outage")
	}
	if !s.healthy() || s.outages.Load() != 0 || len(events.snapshot()) != 0 {
		t.Fatalf("healthy=%v outages=%d events=%v", s.healthy(), s.outages.Load(), events.snapshot())
	}
}

func TestMERTSupervisorLivenessProbeDetectsIdleWorkerFailure(t *testing.T) {
	var idleProbeFailed atomic.Bool
	events := &mertHealthEvents{}
	s := newTestMERTSupervisor(t, func(_ context.Context, wait bool) (bool, error) {
		if !wait && idleProbeFailed.CompareAndSwap(false, true) {
			return true, errors.New("worker exited while idle")
		}
		return true, nil
	}, events)
	s.lastSuccess.Store(time.Now().Add(-time.Hour).UnixNano())
	s.start()
	deadline := time.After(5 * time.Second)
	for len(events.snapshot()) < 2 {
		select {
		case <-deadline:
			t.Fatalf("idle failure and recovery not observed: %v", events.snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got := events.snapshot(); got[0] || !got[1] || !s.healthy() {
		t.Fatalf("events=%v healthy=%v", got, s.healthy())
	}
}

func TestMERTSupervisorWaitReleasesWorkOnShutdown(t *testing.T) {
	blocked := make(chan struct{})
	s := newTestMERTSupervisor(t, func(ctx context.Context, _ bool) (bool, error) {
		<-blocked
		return true, errors.New("still down")
	}, &mertHealthEvents{})
	defer close(blocked)
	s.markDown(errors.New("down"))
	stop := make(chan struct{})
	close(stop)
	if err := s.wait(context.Background(), stop); !errors.Is(err, errMERTUnavailable) {
		t.Fatalf("wait during shutdown=%v, want errMERTUnavailable", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.wait(canceled, nil); !errors.Is(err, errMERTUnavailable) {
		t.Fatalf("wait after cancellation=%v, want errMERTUnavailable", err)
	}
}

func TestMERTSupervisorNotificationsFollowTransitions(t *testing.T) {
	entered, unblock := make(chan struct{}), make(chan struct{})
	events := &mertHealthEvents{}
	s := newMERTSupervisor(context.Background(), func(context.Context, bool) (bool, error) { return true, nil }, func(healthy bool, err error) {
		if healthy {
			close(entered)
			<-unblock
		}
		events.record(healthy, err)
	})
	s.recoveryInitial = time.Millisecond
	defer s.stop()
	s.markDown(errors.New("first outage"))
	<-entered
	// Keep the next recovery from starting during the assertion.
	s.recoveryInitial = time.Hour
	down := make(chan struct{})
	go func() { s.markDown(errors.New("second outage")); close(down) }()
	close(unblock)
	select {
	case <-down:
	case <-time.After(5 * time.Second):
		t.Fatal("second transition blocked")
	}
	got := events.snapshot()
	if len(got) != 3 || got[0] || !got[1] || got[2] || s.healthy() {
		t.Fatalf("events=%v healthy=%v", got, s.healthy())
	}
}
