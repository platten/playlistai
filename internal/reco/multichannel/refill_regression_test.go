package multichannel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestSemanticRefillAdvancesPastExcludedPrefix(t *testing.T) {
	for _, expansion := range []bool{false, true} {
		t.Run(map[bool]string{false: "description", true: "genre-expansion"}[expansion], func(t *testing.T) {
			cat, sem := semanticCatalog(), semanticData()
			cfg := DefaultConfig()
			cfg.SemanticBudget = 2
			r := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), sem, cfg)
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "1", Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "relaxing", Influence: core.InfluencePositive}}}, Controls: core.IntentControls{TotalTrackCount: 1}}
			if expansion {
				intent.Preferences = core.SemanticPreferences{}
				intent.GenreExpansions = []core.GenreExpansion{{Genre: "electronic", Characteristics: "relaxing", RelatedGenres: []string{"ambient"}}}
			}
			excluded := map[string]struct{}{"sleepy": {}, "instrumental": {}}
			got, err := r.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent, AttemptedIDs: excluded})
			if err != nil || len(got) != 1 || got[0].Track.ID != "unknown" || len(excluded) != 2 {
				t.Fatalf("fixed prefix masked deeper match: %+v err=%v", got, err)
			}
		})
	}
}

type interruptedPoolRetriever struct {
	*poolRetriever
	failures int
	cancel   context.CancelFunc
}

func (r *interruptedPoolRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	if len(r.calls) > 0 {
		r.failures++
		if r.cancel != nil {
			r.cancel()
			return nil, ctx.Err()
		}
		return nil, errors.New("synthetic second-page failure")
	}
	return r.poolRetriever.Retrieve(ctx, request)
}

func TestPoolTopupFailureRetainsCandidatesWithoutRelaxingChecks(t *testing.T) {
	for _, mismatches := range []int{0, 1, 2} {
		cat, service, r := recommendationPoolFixture(t, 2, mismatches)
		r.pageSize = 2
		retriever := &interruptedPoolRetriever{poolRetriever: r}
		engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
		engine.retriever = retriever
		got, err := engine.Build(context.Background(), poolIntent(2))
		if err != nil || len(got.Tracks) != 2-mismatches || retriever.failures != 1 || !noticeCode(got.Notices, "retrieval_interrupted") {
			t.Fatalf("mismatches=%d tracks=%+v failures=%d notices=%+v err=%v", mismatches, got.Tracks, retriever.failures, got.Notices, err)
		}
		if got.AudioEvidence == nil || len(got.AudioEvidence.Assessments) != 2 {
			t.Fatal("retained candidates were not checked")
		}
	}
}

func TestPoolTopupParentCancellationDoesNotAnalyzeRetainedCandidates(t *testing.T) {
	cat, service, r := recommendationPoolFixture(t, 2, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = &interruptedPoolRetriever{poolRetriever: r, cancel: cancel}
	got, err := engine.Build(ctx, poolIntent(2))
	if !errors.Is(err, context.Canceled) || len(got.Tracks) != 0 || got.AudioEvidence != nil && len(got.AudioEvidence.Assessments) != 0 {
		t.Fatalf("parent cancellation continued checking: %+v %v", got, err)
	}
}

type interruptedAnalysisStore struct {
	ports.AnalysisStore
	calls     int
	interrupt func()
}

// cacheReadDeadline expires at the cache-read boundary selected by the fixture.
// Keeping its cancellation separate from the generation parent exercises a
// session deadline without racing catalog work against a short wall-clock timer.
type cacheReadDeadline struct{ done chan struct{} }

func (c *cacheReadDeadline) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cacheReadDeadline) Done() <-chan struct{}       { return c.done }
func (c *cacheReadDeadline) Value(any) any               { return nil }
func (c *cacheReadDeadline) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (s *interruptedAnalysisStore) Find(ctx context.Context, catalog, track, key string, model core.AudioModelIdentity) (core.AudioAnalysis, bool, error) {
	s.calls++
	if s.calls == 2 {
		if s.interrupt != nil {
			s.interrupt()
		}
		<-ctx.Done()
		return core.AudioAnalysis{}, false, ctx.Err()
	}
	return s.AnalysisStore.Find(ctx, catalog, track, key, model)
}

func TestAudioInterruptionDuringCacheReadPreservesOnlyAcceptedTracks(t *testing.T) {
	for _, kind := range []string{"session-budget", "stop-checking", "parent-cancellation"} {
		t.Run(kind, func(t *testing.T) {
			cat, service, r := recommendationPoolFixture(t, 2, 0)
			store := &interruptedAnalysisStore{AnalysisStore: service.Store}
			service.Store = store
			intent := poolIntent(2).Normalized()
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := make(chan struct{})
			analysisParent := parent
			switch kind {
			case "session-budget":
				deadline := &cacheReadDeadline{done: make(chan struct{})}
				analysisParent = deadline
				store.interrupt = func() { close(deadline.done) }
			case "stop-checking":
				store.interrupt = func() { close(stop) }
			case "parent-cancellation":
				store.interrupt = cancel
			}
			session, err := service.BeginWithBudget(analysisParent, intent, cat.CatalogVersion(), stop, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
			engine.audioSession, engine.retriever = session, r
			got, _, err := engine.collectIteratively(parent, r.candidates, nil, intent, ports.RecommendationRequest{Intent: intent, StopChecking: stop}, newEligibility(intent, nil, nil), nil, nil, nil, 42)
			if kind == "parent-cancellation" {
				if !errors.Is(err, context.Canceled) || len(got) != 0 {
					t.Fatalf("parent cancellation was swallowed: %+v %v", got, err)
				}
			} else if err != nil || len(got) != 1 || got[0].Track.ID != "p000" {
				t.Fatalf("accepted track lost: %+v %v", got, err)
			}
			if store.calls != 2 || !session.ShouldStop() {
				t.Fatalf("test did not interrupt the cache read: calls=%d stop=%v", store.calls, session.ShouldStop())
			}
			if kind == "session-budget" && (!session.Snapshot().BudgetExhausted || parent.Err() != nil) {
				t.Fatal("session deadline was not distinguished from generation cancellation")
			}
		})
	}
}

func TestExpiredAudioSessionBudgetSkipsCacheReads(t *testing.T) {
	cat, service, r := recommendationPoolFixture(t, 2, 0)
	store := &interruptedAnalysisStore{AnalysisStore: service.Store}
	service.Store = store
	intent := poolIntent(2).Normalized()
	parent := context.Background()
	// A zero duration deterministically exercises BeginWithBudget's own timer,
	// independently of the controlled mid-read deadline in the test above.
	session, err := service.BeginWithBudget(parent, intent, cat.CatalogVersion(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	engine.audioSession, engine.retriever = session, r
	got, _, err := engine.collectIteratively(parent, r.candidates, nil, intent, ports.RecommendationRequest{Intent: intent}, newEligibility(intent, nil, nil), nil, nil, nil, 42)
	if err != nil || len(got) != 0 || store.calls != 0 || !session.Snapshot().BudgetExhausted || parent.Err() != nil {
		t.Fatalf("expired session performed work or canceled its parent: tracks=%d reads=%d snapshot=%+v err=%v", len(got), store.calls, session.Snapshot(), err)
	}
}
