package app

import (
	"context"
	"sync"
)

// audioStartup owns a cancelable initialization lease. A revision keeps an old
// initializer's completion from clearing a newer pending state.
type audioStartup struct {
	mu       sync.Mutex
	revision uint64
	pending  bool
	cancel   context.CancelFunc
}

func (s *audioStartup) start(c *Container, parent context.Context, initialize func(context.Context)) {
	leased, release := c.OperationContext(parent)
	ctx, cancel := context.WithCancel(leased)
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.revision++
	revision := s.revision
	s.pending, s.cancel = true, cancel
	s.mu.Unlock()
	go func() {
		defer release()
		defer cancel()
		defer func() {
			s.mu.Lock()
			if s.revision == revision {
				s.pending = false
				s.cancel = nil
			}
			s.mu.Unlock()
		}()
		initialize(ctx)
	}()
}

func (s *audioStartup) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *audioStartup) loading() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.pending }

// AudioStartupPending distinguishes validation in progress from missing assets.
// Setup and generation must wait rather than suggesting another download.
func (c *Container) AudioStartupPending() bool {
	return c.analysis.startup.loading() || c.enhanced.startup.loading()
}
