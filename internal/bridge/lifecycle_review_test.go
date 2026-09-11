package bridge

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type reviewReaderGate struct {
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
	closed  *atomic.Bool
	t       *testing.T
}

func (g *reviewReaderGate) wait() {
	g.once.Do(func() {
		close(g.entered)
		<-g.resume
		if g.closed != nil && g.closed.Load() {
			g.t.Error("resource closed while reader was using it")
		}
	})
}

type reviewResolver struct {
	ports.ReferenceResolver
	gate *reviewReaderGate
}

func (r reviewResolver) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	r.gate.wait()
	return r.ReferenceResolver.ResolveReference(ref)
}

type reviewCatalog struct {
	ports.Catalog
	gate *reviewReaderGate
}

func (c reviewCatalog) Vectors(id string) (ports.Vectors, bool) {
	c.gate.wait()
	return c.Catalog.Vectors(id)
}

type reviewProfiles struct {
	ports.ProfileStore
	saves atomic.Int32
}

func (p *reviewProfiles) SaveProfile(ctx context.Context, profile core.TasteProfile) error {
	p.saves.Add(1)
	return p.ProfileStore.SaveProfile(ctx, profile)
}

func reviewWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not reach test barrier")
	}
}
func reviewShutdownStarted(t *testing.T, c *app.Container) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not start")
		}
		runtime.Gosched()
	}
}

func TestCloseWaitsForBridgeResolutionAndProfileReaders(t *testing.T) {
	for _, kind := range []string{"parse", "profile"} {
		t.Run(kind, func(t *testing.T) {
			c := newLoadedContainer(t)
			a := New(c, nil)
			var closed atomic.Bool
			c.RegisterCloser(func() error { closed.Store(true); return nil })
			gate := &reviewReaderGate{entered: make(chan struct{}), resume: make(chan struct{}), closed: &closed, t: t}
			defer close(gate.resume)
			snapshot := a.runtime()
			if kind == "parse" {
				snapshot.Resolver = reviewResolver{snapshot.Resolver, gate}
			} else {
				snapshot.Catalog = reviewCatalog{snapshot.Catalog, gate}
				if _, err := c.Feedback.RecordFeedback(context.Background(), core.FeedbackEvent{TrackID: "seed0001", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable}); err != nil {
					t.Fatal(err)
				}
			}
			a.runtime = func() app.RuntimeSnapshot { return snapshot }
			result := make(chan error, 1)
			go func() {
				if kind == "parse" {
					_, err := a.ParseIntent(context.Background(), "like Justice")
					result <- err
				} else {
					_, err := a.GetTasteProfile(context.Background(), "", "")
					result <- err
				}
			}()
			reviewWait(t, gate.entered)
			shutdown := make(chan error, 1)
			go func() { shutdown <- c.Close() }()
			reviewShutdownStarted(t, c)
			select {
			case err := <-shutdown:
				t.Fatalf("shutdown closed resources before reader released: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			gate.resume <- struct{}{}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("shutdown result: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("reader did not finish")
			}
			select {
			case err := <-shutdown:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not finish")
			}
			if !closed.Load() {
				t.Fatal("resources never closed")
			}
		})
	}
}

func TestClearedTasteRejectsProfileAlreadyBeingProjected(t *testing.T) {
	c := newLoadedContainer(t)
	a := New(c, nil)
	if _, err := c.Feedback.RecordFeedback(context.Background(), core.FeedbackEvent{TrackID: "seed0001", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable}); err != nil {
		t.Fatal(err)
	}
	profiles := &reviewProfiles{ProfileStore: c.Profiles}
	c.Profiles = profiles
	gate := &reviewReaderGate{entered: make(chan struct{}), resume: make(chan struct{}), t: t}
	defer close(gate.resume)
	snapshot := a.runtime()
	snapshot.Catalog = reviewCatalog{snapshot.Catalog, gate}
	a.runtime = func() app.RuntimeSnapshot { return snapshot }
	result := make(chan error, 1)
	go func() { _, err := a.GetTasteProfile(context.Background(), "", ""); result <- err }()
	reviewWait(t, gate.entered)
	if err := a.ClearTasteData(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate.resume <- struct{}{}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stale profile was returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("profile projection did not finish")
	}
	if profiles.saves.Load() != 0 {
		t.Fatal("old feedback was persisted again after clear")
	}
	events, err := c.Feedback.ListFeedback(context.Background(), ports.FeedbackQuery{})
	if err != nil || len(events) != 0 {
		t.Fatalf("feedback resurrected: %v %v", events, err)
	}
}
