package ports

import (
	"context"
	"time"
)

const EnhancedGenerationLimit = 10 * time.Minute
const EnhancedWorkLimit = EnhancedGenerationLimit - 15*time.Second
const AutomaticGenerationLimit = 2 * time.Minute
const AutomaticAssemblyReserve = 5 * time.Second

type generationBudgetKey struct{}
type generationWorkKey struct{}
type generationBudget struct {
	started, deadline time.Time
	assemblyReserve   time.Duration
}

// WithGenerationBudget starts the Enhanced submission clock once. Parsing,
// resolution, retrieval and assembly share this deadline; nested callers never
// extend it. Ordinary cancellation remains cancellation, not stop-and-keep.
func WithGenerationBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithGenerationBudgetSince(ctx, time.Now())
}

// WithGenerationBudgetSince carries the desktop submission time through its
// parse and generate calls. Future/absent client clocks cannot extend the cap.
func WithGenerationBudgetSince(ctx context.Context, started time.Time) (context.Context, context.CancelFunc) {
	return withGenerationBudget(ctx, started, EnhancedGenerationLimit, 0)
}

// WithAutomaticGenerationBudget shares one bounded clock across interpretation,
// preparation, retrieval, and the final local assembly pass.
func WithAutomaticGenerationBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithAutomaticGenerationBudgetSince(ctx, time.Now())
}

func WithAutomaticGenerationBudgetSince(ctx context.Context, started time.Time) (context.Context, context.CancelFunc) {
	return withGenerationBudget(ctx, started, AutomaticGenerationLimit, AutomaticAssemblyReserve)
}

func withGenerationBudget(ctx context.Context, started time.Time, limit, reserve time.Duration) (context.Context, context.CancelFunc) {
	if b, ok := ctx.Value(generationBudgetKey{}).(generationBudget); ok {
		if !b.started.Add(limit).Before(b.deadline) {
			return context.WithCancel(ctx)
		}
		// An automatic caller can tighten an older enclosing budget; no caller
		// can restart the clock or extend an existing deadline.
		started = b.started
	}
	if started.IsZero() || started.After(time.Now()) {
		started = time.Now()
	}
	deadline := started.Add(limit)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	ctx = context.WithValue(ctx, generationBudgetKey{}, generationBudget{started: started, deadline: deadline, assemblyReserve: reserve})
	return context.WithDeadline(ctx, deadline)
}

// GenerationWorkContext reserves five seconds for Automatic assembly and up
// to fifteen for legacy Enhanced requests. Nested callers share the reserve.
func GenerationWorkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Value(generationWorkKey{}) != nil {
		return context.WithCancel(ctx)
	}
	if b, ok := ctx.Value(generationBudgetKey{}).(generationBudget); ok {
		deadline := b.deadline
		reserve := min(15*time.Second, max(0, deadline.Sub(b.started))/20)
		fraction := time.Duration(20)
		if b.assemblyReserve > 0 {
			fraction = 2
			reserve = min(b.assemblyReserve, max(0, deadline.Sub(b.started))/fraction)
		}
		if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
			deadline = parentDeadline
			reserve = min(reserve, max(0, time.Until(deadline))/fraction)
		}
		ctx = context.WithValue(ctx, generationWorkKey{}, true)
		return context.WithDeadline(ctx, deadline.Add(-reserve))
	}
	return context.WithCancel(ctx)
}

func GenerationStarted(ctx context.Context) time.Time {
	b, _ := ctx.Value(generationBudgetKey{}).(generationBudget)
	return b.started
}
