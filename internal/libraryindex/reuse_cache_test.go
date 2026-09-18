package libraryindex

import (
	"context"
	"testing"
	"time"
)

func TestMERTReuseCacheLoadsAndPublishesAfterLeaderFinishes(t *testing.T) {
	contract := "audio/v1"
	seeded := ReusableMERT{Vector: []byte{1, 2, 3, 4}, Dimension: 1}
	cache := newMERTReuseCache(map[string]ReusableMERT{mertReuseKey("seeded", contract): seeded})
	if got, found, flight, err := cache.claim(context.Background(), "seeded", contract); err != nil || !found || flight != nil || got.Dimension != 1 {
		t.Fatalf("seeded claim: found=%v flight=%v got=%+v err=%v", found, flight, got, err)
	}

	_, found, leader, err := cache.claim(context.Background(), "new", contract)
	if err != nil || found || leader == nil {
		t.Fatalf("leader claim: found=%v flight=%v err=%v", found, leader, err)
	}
	type result struct {
		value ReusableMERT
		found bool
		err   error
	}
	waiter := make(chan result, 1)
	go func() {
		value, found, _, err := cache.claim(context.Background(), "new", contract)
		waiter <- result{value: value, found: found, err: err}
	}()
	select {
	case got := <-waiter:
		t.Fatalf("waiter completed before durable publish: %+v", got)
	default:
	}
	published := ReusableMERT{Vector: []byte{5, 6, 7, 8}, Dimension: 1}
	cache.store("new", contract, published)
	cache.finish("new", contract, leader)
	select {
	case got := <-waiter:
		if got.err != nil || !got.found || got.value.Dimension != 1 || len(got.value.Vector) != 4 {
			t.Fatalf("waiter result: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released after publish")
	}
}
