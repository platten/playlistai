package multichannel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type lifecyclePrefetchStream struct {
	fixtureDiscovery
	t                *testing.T
	cancel           context.CancelFunc
	ctx              context.Context
	done             chan struct{}
	started, stopped bool
	once             sync.Once
}

func (s *lifecyclePrefetchStream) OpenCandidates(core.MusicIntent, ports.Catalog, ports.ReferenceResolver) ports.MusicCandidateStream {
	return s
}
func (s *lifecyclePrefetchStream) Snapshot() *core.KnowledgeSnapshot {
	if s.t != nil && s.started && !s.stopped {
		s.t.Error("snapshot finalized before prefetch joined")
	}
	return s.fixtureDiscovery.Snapshot()
}
func (s *lifecyclePrefetchStream) StartPrefetch(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.ctx, s.started = ctx, true
	s.done = make(chan struct{})
	go func() { <-ctx.Done(); close(s.done) }()
}
func (s *lifecyclePrefetchStream) StopPrefetch() {
	s.once.Do(func() {
		if s.started {
			s.cancel()
			<-s.done
		}
		s.stopped = true
	})
}

func TestPrefetchStopsBeforeSnapshotAndCatalogRelease(t *testing.T) {
	cat, intent := localPriorityFixture()
	source := &lifecyclePrefetchStream{t: t}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(source)
	r := &prefetchObservingRetriever{source: source, candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one")), t: t}
	released := false
	o.WithIntentOverlayProvider(func(_ context.Context, _ core.MusicIntent, catalog ports.Catalog, resolver ports.ReferenceResolver, _ ports.CandidateRetriever) (RequestOverlay, error) {
		return RequestOverlay{Catalog: catalog, Resolver: resolver, Retriever: r, Release: func() {
			released = true
			if !source.stopped {
				t.Error("catalog released before prefetch joined")
			}
		}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := o.Build(ctx, intent)
	if err != nil || len(got.Tracks) != 1 || !released || !source.stopped || source.pulls != 0 {
		t.Fatalf("local result or cleanup changed: tracks=%v err=%v released=%v stopped=%v pulls=%d", got.Tracks, err, released, source.stopped, source.pulls)
	}
	if len(got.Intent.Knowledge.Discovery) != 0 {
		t.Fatal("unused prefetch entered snapshot")
	}
}

type prefetchObservingRetriever struct {
	source     *lifecyclePrefetchStream
	candidates []core.Candidate
	t          *testing.T
}

func (*prefetchObservingRetriever) SupportsIntentMetadata() bool { return true }
func (r *prefetchObservingRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	if !r.source.started || r.source.stopped {
		r.t.Error("prefetch did not overlap local retrieval")
	}
	return r.candidates, nil
}

func TestPrefetchStopCancelsFetchWithoutCancelingGeneration(t *testing.T) {
	source := &lifecyclePrefetchStream{}
	stop := make(chan struct{})
	parent := context.Background()
	finish := startCandidatePrefetch(parent, source, stop)
	close(stop)
	select {
	case <-source.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel prefetch")
	}
	finish()
	if parent.Err() != nil || !source.stopped {
		t.Fatal("stop changed parent or failed to join")
	}
}
