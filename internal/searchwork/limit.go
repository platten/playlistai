// Package searchwork bounds active CPU scans across catalogs and generations.
// Only leaf scan workers acquire permits; job coordinators must never hold one.
package searchwork

import (
	"context"
	"runtime"
	"sync"
)

var scans = struct {
	sync.Mutex
	active  int
	changed chan struct{}
}{changed: make(chan struct{})}

// Jobs is the default number of independent search jobs per coordinator.
func Jobs() int { return min(8, runtime.GOMAXPROCS(0)) }

// Acquire waits for a process-wide CPU scan permit. The returned release must
// be called exactly once, after the scan, including on cancellation or error.
func Acquire(ctx context.Context) (release func(), err error) {
	for {
		scans.Lock()
		if err := ctx.Err(); err != nil {
			scans.Unlock()
			return nil, err
		}
		if scans.active < runtime.GOMAXPROCS(0) {
			scans.active++
			scans.Unlock()
			return func() {
				scans.Lock()
				scans.active--
				close(scans.changed)
				scans.changed = make(chan struct{})
				scans.Unlock()
			}, nil
		}
		changed := scans.changed
		scans.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
