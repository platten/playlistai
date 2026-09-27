package ports

import (
	"context"
	"testing"
	"time"
)

func TestAutomaticBudgetSharesSubmissionAndAssemblyReserve(t *testing.T) {
	started := time.Now().Add(-3 * time.Second)
	ctx, cancel := WithAutomaticGenerationBudgetSince(context.Background(), started)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if !deadline.Equal(started.Add(2*time.Minute)) || !GenerationStarted(ctx).Equal(started) {
		t.Fatal("automatic submission clock was reset")
	}
	work, finish := GenerationWorkContext(ctx)
	defer finish()
	workDeadline, _ := work.Deadline()
	if !workDeadline.Equal(deadline.Add(-5 * time.Second)) {
		t.Fatalf("assembly reserve = %v, want 5s", deadline.Sub(workDeadline))
	}
	nested, release := GenerationWorkContext(work)
	defer release()
	if got, _ := nested.Deadline(); !got.Equal(workDeadline) {
		t.Fatal("nested work reserved time twice")
	}
	legacy, stop := WithGenerationBudget(ctx)
	defer stop()
	if got, _ := legacy.Deadline(); !got.Equal(deadline) {
		t.Fatal("legacy helper extended automatic deadline")
	}
}

func TestAutomaticBudgetTightensLegacyBudgetAndPreservesCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	started := time.Now().Add(-2 * time.Second)
	legacy, stopLegacy := WithGenerationBudgetSince(parent, started)
	defer stopLegacy()
	ctx, cancel := WithAutomaticGenerationBudget(legacy)
	defer cancel()
	if got, _ := ctx.Deadline(); !got.Equal(started.Add(AutomaticGenerationLimit)) {
		t.Fatal("automatic engine inherited a ten-minute budget")
	}
	cancelParent()
	if ctx.Err() != context.Canceled {
		t.Fatalf("cancellation = %v", ctx.Err())
	}
}
