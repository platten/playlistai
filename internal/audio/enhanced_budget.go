package audio

import (
	"context"
	"sync"
	"time"
)

const EnhancedTrackLimit = 24
const EnhancedTimeLimit = 2 * time.Minute

type enhancedBudgetKey struct{}

// EnhancedBudget belongs to a single generation. DSP and MERT share the same
// track admissions and deadline across preview checking and final assembly.
// Neither expiry nor refusal cancels the enclosing CLAP/provider context.
type EnhancedBudget struct {
	mu       sync.Mutex
	tracks   map[string]bool
	limit    int
	deadline time.Time
	duration time.Duration
}

// WithEnhancedBudget preserves an existing budget so helper layers cannot
// reset a generation's counter or extend its original deadline.
func WithEnhancedBudget(ctx context.Context, maxTracks int, duration time.Duration) context.Context {
	if EnhancedBudgetFor(ctx) != nil {
		return ctx
	}
	maxTracks = max(0, min(maxTracks, EnhancedTrackLimit))
	duration = max(time.Duration(0), min(duration, EnhancedTimeLimit))
	return context.WithValue(ctx, enhancedBudgetKey{}, &EnhancedBudget{tracks: map[string]bool{}, limit: maxTracks, deadline: time.Now().Add(duration)})
}

// WithLazyEnhancedBudget excludes discovery and interpretation from optional
// inference time. The first admission/context starts the deadline once; the
// enclosing request still owns its independent deadline and cancellation.
func WithLazyEnhancedBudget(ctx context.Context, maxTracks int, duration time.Duration) context.Context {
	if EnhancedBudgetFor(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, enhancedBudgetKey{}, &EnhancedBudget{
		tracks: map[string]bool{}, limit: max(0, min(maxTracks, EnhancedTrackLimit)),
		duration: max(time.Duration(0), min(duration, EnhancedTimeLimit)),
	})
}

func (b *EnhancedBudget) startLocked() {
	if b.deadline.IsZero() {
		b.deadline = time.Now().Add(b.duration)
	}
}

func EnhancedBudgetFor(ctx context.Context) *EnhancedBudget {
	b, _ := ctx.Value(enhancedBudgetKey{}).(*EnhancedBudget)
	return b
}

// Allow reserves a distinct track before optional enhanced acquisition. The
// same track may use both extractors without consuming another admission.
// Callers may read already cached evidence without calling Allow.
func (b *EnhancedBudget) Allow(trackID string) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startLocked()
	if trackID == "" || !time.Now().Before(b.deadline) {
		return false
	}
	if b.tracks[trackID] {
		return true
	}
	if len(b.tracks) >= b.limit {
		return false
	}
	b.tracks[trackID] = true
	return true
}

// Context applies the enhanced deadline to derived work only. Always cancel
// the returned child after extraction; continue CLAP on the original context.
func (b *EnhancedBudget) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	if b == nil {
		return context.WithCancel(ctx)
	}
	b.mu.Lock()
	b.startLocked()
	deadline := b.deadline
	b.mu.Unlock()
	return context.WithDeadline(ctx, deadline)
}

func (b *EnhancedBudget) Used() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.tracks)
}
