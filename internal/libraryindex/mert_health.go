package libraryindex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// errMERTUnavailable marks work interrupted by a MERT worker outage rather
// than by the track. Such jobs return to the queue without a retry charge.
var errMERTUnavailable = errors.New("library indexer: MERT worker unavailable; audio analysis paused")

const (
	mertLivenessInterval  = 30 * time.Second
	mertRecoveryInitial   = time.Second
	mertRecoveryMaximum   = 30 * time.Second
	mertOutageIssueDetail = "MERT worker failed its health check; audio analysis is paused until it recovers"
)

// mertProbe acquires a worker and runs its fixture health check. With wait
// false it reports ok=false instead of waiting when every worker is busy.
type mertProbe func(ctx context.Context, wait bool) (ok bool, err error)

// mertSupervisor gates audio analysis on a healthy MERT worker. An outage is
// declared only when a health check fails, so a track that merely crashes the
// worker is still charged to that track. During an outage a single recovery
// loop restarts and health-checks a worker with backoff; analysis resumes only
// after a check passes.
type mertSupervisor struct {
	probe    mertProbe
	onChange func(healthy bool, err error)

	liveness        time.Duration
	recoveryInitial time.Duration
	recoveryMaximum time.Duration

	eventMu       sync.Mutex // serializes state transitions with their notifications
	outageStarted time.Time
	outageTotal   time.Duration
	mu            sync.Mutex
	ready         chan struct{} // closed while healthy
	cancel        context.CancelFunc
	ctx           context.Context
	wg            sync.WaitGroup

	lastSuccess atomic.Int64
	outages     atomic.Int64
}

func newMERTSupervisor(ctx context.Context, probe mertProbe, onChange func(bool, error)) *mertSupervisor {
	ctx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	close(ready)
	s := &mertSupervisor{probe: probe, onChange: onChange, liveness: mertLivenessInterval,
		recoveryInitial: mertRecoveryInitial, recoveryMaximum: mertRecoveryMaximum, ready: ready, ctx: ctx, cancel: cancel}
	s.lastSuccess.Store(time.Now().UnixNano())
	return s
}

// start launches the idle liveness probe. Busy workers prove liveness through
// their own 30-second response watchdog, so probes run only after a quiet
// interval and never wait for a worker.
func (s *mertSupervisor) start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.liveness)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
			if !s.healthy() || time.Since(time.Unix(0, s.lastSuccess.Load())) < s.liveness {
				continue
			}
			ok, err := s.probe(s.ctx, false)
			if s.ctx.Err() != nil {
				return
			}
			if !ok {
				continue
			}
			if err != nil {
				s.markDown(err)
				continue
			}
			s.succeeded()
		}
	}()
}

// stop cancels probes and waits for them so the pool can be closed safely.
func (s *mertSupervisor) stop() {
	// Cancel under mu: markDown checks cancellation and registers its recovery
	// goroutine under the same lock, so none can start once Wait begins.
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *mertSupervisor) succeeded() { s.lastSuccess.Store(time.Now().UnixNano()) }

func (s *mertSupervisor) healthy() bool {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	select {
	case <-ready:
		return true
	default:
		return false
	}
}

// wait blocks until MERT is healthy. Shutdown or cancellation while paused
// returns errMERTUnavailable so the held job is released, not failed.
func (s *mertSupervisor) wait(ctx context.Context, stop <-chan struct{}) error {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	select {
	case <-ready:
		return nil
	default:
	}
	select {
	case <-ready:
		return nil
	case <-stop:
		return errMERTUnavailable
	case <-ctx.Done():
		return errors.Join(errMERTUnavailable, context.Cause(ctx))
	}
}

// confirmOutage health-checks the worker whose request just failed. The
// check restarts a dead process. It reports true, after pausing analysis,
// only when the fresh worker is also unhealthy.
func (s *mertSupervisor) confirmOutage(ctx context.Context, check func(context.Context) error) bool {
	err := check(ctx)
	if err == nil {
		s.succeeded()
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	s.markDown(err)
	return true
}

func (s *mertSupervisor) markDown(err error) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	s.mu.Lock()
	select {
	case <-s.ready:
	default:
		s.mu.Unlock()
		return // an outage and its recovery loop are already active
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.ready = make(chan struct{})
	s.outageStarted = time.Now()
	s.outages.Add(1)
	s.wg.Add(1)
	s.mu.Unlock()
	if s.onChange != nil {
		s.onChange(false, err)
	}
	go s.recover()
}

func (s *mertSupervisor) recover() {
	defer s.wg.Done()
	delay := s.recoveryInitial
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
		ok, err := s.probe(s.ctx, true)
		if s.ctx.Err() != nil {
			return
		}
		if ok && err == nil {
			s.eventMu.Lock()
			s.succeeded()
			s.mu.Lock()
			close(s.ready)
			s.outageTotal += time.Since(s.outageStarted)
			s.outageStarted = time.Time{}
			s.mu.Unlock()
			if s.onChange != nil {
				s.onChange(true, nil)
			}
			s.eventMu.Unlock()
			return
		}
		delay = min(2*delay, s.recoveryMaximum)
	}
}

func (s *mertSupervisor) outageDuration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.outageTotal
	if !s.outageStarted.IsZero() {
		total += time.Since(s.outageStarted)
	}
	return total
}
