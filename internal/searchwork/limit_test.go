package searchwork

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSharedCeilingAndQueuedCancellation(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	first, err := Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		release, err := Acquire(ctx)
		if release != nil {
			release()
		}
		queued <- err
	}()
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	first()
	second()
	var active atomic.Int32
	var exceeded atomic.Bool
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				release, err := Acquire(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if active.Add(1) > 2 {
					exceeded.Store(true)
				}
				runtime.Gosched()
				active.Add(-1)
				release()
			}
		}()
	}
	wg.Wait()
	if exceeded.Load() {
		t.Fatal("global CPU scan ceiling exceeded")
	}
	if Jobs() != 2 {
		t.Fatal("job count does not honor GOMAXPROCS")
	}
}
