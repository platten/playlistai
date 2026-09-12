package audio

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestEnhancedBudgetSharedAdmissionsAndLegacyContext(t *testing.T) {
	parent := context.Background()
	ctx := WithEnhancedBudget(parent, 24, 2*time.Minute)
	budget := EnhancedBudgetFor(ctx)
	if EnhancedBudgetFor(WithEnhancedBudget(ctx, 24, 2*time.Minute)) != budget {
		t.Fatal("helper reset request budget")
	}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); budget.Allow(fmt.Sprint(i)) }()
	}
	wg.Wait()
	if budget.Used() != 24 {
		t.Fatalf("admitted %d", budget.Used())
	}
	if budget.Allow("new") {
		t.Fatal("budget overflow")
	}
	child, cancel := budget.Context(ctx)
	cancel()
	if child.Err() != context.Canceled || parent.Err() != nil || ctx.Err() != nil {
		t.Fatal("derived work canceled legacy context")
	}
	expired := EnhancedBudgetFor(WithEnhancedBudget(parent, 24, 0))
	if expired.Allow("a") {
		t.Fatal("deadline ignored")
	}
	child, cancel = expired.Context(parent)
	defer cancel()
	if child.Err() != context.DeadlineExceeded || parent.Err() != nil {
		t.Fatal("deadline leaked")
	}
}

func TestEnhancedBudgetSameTrackCostsOnce(t *testing.T) {
	budget := EnhancedBudgetFor(WithEnhancedBudget(context.Background(), 1, time.Minute))
	first, second := budget.Allow("same"), budget.Allow("same")
	if !first || !second || budget.Used() != 1 || budget.Allow("other") {
		t.Fatal("DSP and MERT charged twice")
	}
}
