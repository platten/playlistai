package searchwork

import (
	"context"
	"sync"
)

// Run executes independent indexed jobs and joins every worker. Callers own the
// result slots and commit them in index order. It never holds a CPU permit.
func Run(ctx context.Context, count int, run func(int)) {
	if count == 1 {
		if ctx.Err() == nil {
			run(0)
		}
		return
	}
	if count == 0 {
		return
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(count, Jobs()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() == nil {
					run(index)
				}
			}
		}()
	}
dispatch:
	for index := range count {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
}
