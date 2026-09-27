package ports

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestGenerationBudgetIncludesEarlierWorkAndReservesCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := WithGenerationBudget(context.Background())
		defer cancel()
		started := GenerationStarted(ctx)
		time.Sleep(EnhancedWorkLimit - 5*time.Second) // parsing/resolution belong to this clock
		nested, finish := WithGenerationBudget(ctx)
		defer finish()
		work, finishWork := GenerationWorkContext(nested)
		defer finishWork()
		deadline, _ := nested.Deadline()
		workDeadline, _ := work.Deadline()
		if deadline != started.Add(EnhancedGenerationLimit) || workDeadline != started.Add(EnhancedWorkLimit) {
			t.Fatal("nested work reset the submission clock")
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if work.Err() != context.DeadlineExceeded || ctx.Err() != nil {
			t.Fatal("assembly reserve was not preserved")
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatal("generation cap missing")
		}
	})
}

func TestShorterGenerationBudgetStillReservesAssembly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 5*time.Minute)
		defer stop()
		ctx, cancel := WithGenerationBudget(parent)
		defer cancel()
		work, finish := GenerationWorkContext(ctx)
		defer finish()
		deadline, _ := ctx.Deadline()
		workDeadline, _ := work.Deadline()
		if workDeadline != deadline.Add(-15*time.Second) {
			t.Fatal("shorter evaluation budget lost assembly reserve")
		}
		time.Sleep(285 * time.Second)
		synctest.Wait()
		if work.Err() != context.DeadlineExceeded || ctx.Err() != nil {
			t.Fatal("shorter generation did not preserve checked results")
		}
	})
}

func TestDiagnosticBudgetCanStartWorkAndNestedWorkDoesNotSpendReserveTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer stop()
		ctx, cancel := WithGenerationBudget(parent)
		defer cancel()
		work, finish := GenerationWorkContext(ctx)
		defer finish()
		nested, finishNested := GenerationWorkContext(work)
		defer finishNested()
		deadline, _ := work.Deadline()
		nestedDeadline, _ := nested.Deadline()
		if work.Err() != nil || deadline != nestedDeadline || deadline != GenerationStarted(ctx).Add(190*time.Millisecond) {
			t.Fatal("short diagnostics expired immediately or nested work consumed the reserve twice")
		}
	})
}

func TestGenerationBudgetPreservesCancelAndRejectsFutureClock(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := WithGenerationBudgetSince(parent, time.Now().Add(time.Hour))
	defer cancel()
	deadline, _ := ctx.Deadline()
	if time.Until(deadline) > EnhancedGenerationLimit {
		t.Fatal("future client clock extended budget")
	}
	work, finish := GenerationWorkContext(ctx)
	defer finish()
	cancelParent()
	if ctx.Err() != context.Canceled || work.Err() != context.Canceled {
		t.Fatal("cancellation lost")
	}
}
