package app

import (
	"context"
	"sync"
)

// OperationContext leases the runtime until release. Shutdown cancels work
// before waiting for leases, so mapped catalog vectors and model workers cannot
// be closed while a recommendation still uses them. Release is idempotent.
func (c *Container) OperationContext(parent context.Context) (context.Context, func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	if c.lifetime == nil {
		c.lifetime, c.stopLifetime = context.WithCancel(context.Background())
	}
	c.work.Add(1) // guarded with closed: Add can never race shutdown's Wait
	lifetime := c.lifetime
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	var once sync.Once
	return ctx, func() { once.Do(func() { stop(); cancel(); c.work.Done() }) }
}
