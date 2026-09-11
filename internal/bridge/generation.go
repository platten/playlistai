package bridge

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/platten/playlistai/internal/core"
)

type generationKey struct{}
type liveGeneration struct {
	ID   string
	stop chan struct{}
	once sync.Once
}
type liveGenerations struct {
	mu     sync.Mutex
	active map[string]*liveGeneration
}

var nextGeneration atomic.Uint64

func (a *API) beginGeneration(ctx context.Context, id string) (context.Context, func()) {
	if id == "" {
		id = fmt.Sprintf("generation-%d", nextGeneration.Add(1))
	}
	g := &liveGeneration{ID: id, stop: make(chan struct{})}
	a.live.mu.Lock()
	if a.live.active == nil {
		a.live.active = map[string]*liveGeneration{}
	}
	if previous := a.live.active[id]; previous != nil {
		previous.once.Do(func() { close(previous.stop) })
	}
	a.live.active[id] = g
	a.live.mu.Unlock()
	return context.WithValue(ctx, generationKey{}, g), func() {
		a.live.mu.Lock()
		defer a.live.mu.Unlock()
		if a.live.active[id] == g {
			delete(a.live.active, id)
		}
	}
}
func generationFromContext(ctx context.Context) *liveGeneration {
	g, _ := ctx.Value(generationKey{}).(*liveGeneration)
	return g
}

// StopAndKeepCheckedTracks stops candidate discovery and audio analysis.
// Ranking and sequencing finish over checked tracks; ordinary cancellation
// still discards the result.
func (a *API) StopAndKeepCheckedTracks(generationID string) {
	a.live.mu.Lock()
	defer a.live.mu.Unlock()
	if g := a.live.active[generationID]; g != nil {
		g.once.Do(func() { close(g.stop) })
	}
}

func generationProgress(ctx context.Context) *WailsProgress {
	p := NewWailsProgress()
	if g := generationFromContext(ctx); g != nil {
		p.generationID = g.ID
	}
	return p
}

func withEvidenceIdentity(identity *Reproducibility, snapshot *core.AudioEvidenceSnapshot) {
	if snapshot == nil {
		return
	}
	identity.EvidenceSnapshot = snapshot.ID
	identity.ID = audioIdentity(identity.ID, snapshot.ID)
}
