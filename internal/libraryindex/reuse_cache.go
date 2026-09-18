package libraryindex

import (
	"context"
	"sync"
)

type reuseFlight struct{ done chan struct{} }

// mertReuseCache is the analysis hot-path recording index. It is populated
// from durable state before workers start, then updated only after a new MERT
// result commits successfully. SQLite is therefore not consulted once per
// candidate file merely to discover that most recordings are unique.
type mertReuseCache struct {
	mu      sync.Mutex
	values  map[string]ReusableMERT
	flights map[string]*reuseFlight
}

func newMERTReuseCache(seed map[string]ReusableMERT) *mertReuseCache {
	values := make(map[string]ReusableMERT, len(seed))
	for key, value := range seed {
		values[key] = value
	}
	return &mertReuseCache{values: values, flights: make(map[string]*reuseFlight)}
}

func mertReuseKey(recordingKey, contract string) string {
	return contract + "\x00" + recordingKey
}

func (c *mertReuseCache) claim(ctx context.Context, recordingKey, contract string) (ReusableMERT, bool, *reuseFlight, error) {
	if recordingKey == "" {
		return ReusableMERT{}, false, nil, nil
	}
	key := mertReuseKey(recordingKey, contract)
	for {
		c.mu.Lock()
		if cached, found := c.values[key]; found {
			c.mu.Unlock()
			return cached, true, nil, nil
		}
		if existing := c.flights[key]; existing != nil {
			c.mu.Unlock()
			select {
			case <-existing.done:
				continue
			case <-ctx.Done():
				return ReusableMERT{}, false, nil, ctx.Err()
			}
		}
		flight := &reuseFlight{done: make(chan struct{})}
		c.flights[key] = flight
		c.mu.Unlock()
		return ReusableMERT{}, false, flight, nil
	}
}

func (c *mertReuseCache) store(recordingKey, contract string, value ReusableMERT) {
	if recordingKey == "" || len(value.Vector) == 0 {
		return
	}
	value.Vector = append([]byte(nil), value.Vector...)
	c.mu.Lock()
	c.values[mertReuseKey(recordingKey, contract)] = value
	c.mu.Unlock()
}

func (c *mertReuseCache) finish(recordingKey, contract string, flight *reuseFlight) {
	if flight == nil {
		return
	}
	key := mertReuseKey(recordingKey, contract)
	c.mu.Lock()
	if c.flights[key] == flight {
		delete(c.flights, key)
		close(flight.done)
	}
	c.mu.Unlock()
}
