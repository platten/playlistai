package multichannel

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type countedSelector struct {
	ports.CandidateSelector
	calls int
}

func (s *countedSelector) Select(ctx context.Context, candidates []core.Candidate, request ports.SelectionRequest) (ports.SelectionResult, error) {
	s.calls++
	return s.CandidateSelector.Select(ctx, candidates, request)
}

func TestGenerationReusesCompletedAssemblyAndNeverSharesItsCache(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{tracks: refs(cat, "last")})
	selector := &countedSelector{CandidateSelector: engine.selector}
	engine.selector = selector
	got, err := engine.Build(context.Background(), testIntent(1))
	if err != nil || len(got.Tracks) != 1 || selector.calls != 1 {
		t.Fatalf("completion was recomputed: tracks=%+v selections=%d err=%v", got.Tracks, selector.calls, err)
	}
	if engine.assemblyCache != nil {
		t.Fatal("request cache escaped into shared orchestrator")
	}
	engine.candidateSource = &fixtureDiscovery{tracks: refs(cat, "last")}
	again, err := engine.Build(context.Background(), testIntent(1))
	if err != nil || !reflect.DeepEqual(got.Tracks, again.Tracks) || selector.calls != 2 {
		t.Fatalf("new generation reused old request cache: selections=%d err=%v", selector.calls, err)
	}
}

func TestAudioGenerationReusesCompletedAssemblyAfterFinalEvidenceChecks(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		t.Run(string(mode), func(t *testing.T) {
			cat, service, retriever := recommendationPoolFixture(t, 20, 0)
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
			engine.retriever = retriever
			selector := &countedSelector{CandidateSelector: engine.selector}
			engine.selector = selector
			intent := poolIntent(10)
			intent.Controls.RecommendationMode = mode
			got, err := engine.Build(context.Background(), intent)
			if err != nil || len(got.Tracks) != 10 || got.Outcome.State != core.OutcomeFulfilled || selector.calls != 1 {
				t.Fatalf("audio completion was recomputed or evidence lost: tracks=%d selections=%d outcome=%+v err=%v", len(got.Tracks), selector.calls, got.Outcome, err)
			}
			if got.AudioEvidence == nil || len(got.AudioEvidence.Assessments) != 10 {
				t.Fatalf("cached assembly lost final preview evidence: %+v", got.AudioEvidence)
			}
		})
	}
}

func TestAssemblyCacheKeyTracksEveryMutableSelectionInput(t *testing.T) {
	for _, change := range []string{"candidate", "intent", "profile", "recent", "references", "required", "waypoints", "seed", "config", "knowledge", "policy", "audio"} {
		t.Run(change, func(t *testing.T) {
			cat := testCatalog()
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
			intent := testIntent(1)
			request := ports.RecommendationRequest{Intent: intent}
			candidates := candidatesForTracks(refs(cat, "last", "other"))
			var references, required, waypoints []core.TrackRef
			seed := int64(42)
			engine.knowledge = &core.KnowledgeSnapshot{ID: "before"}
			before := engine.assemblyKey(candidates, intent, request, references, required, waypoints, seed)
			switch change {
			case "candidate":
				candidates[0].Scores.SemanticMatch = .4
			case "intent":
				intent.Controls.ArtistDiversity = .8
			case "profile":
				request.Profile.RequestPositive.Audio = []float32{1, 0}
			case "recent":
				request.RecentSelections = []core.TrackRef{{ID: "audio"}}
			case "references":
				references = refs(cat, "seed")
			case "required":
				required = refs(cat, "last")
			case "waypoints":
				waypoints = refs(cat, "last")
			case "seed":
				seed++
			case "config":
				engine.cfg.SelectionMinimumRelevance = .2
			case "knowledge":
				engine.knowledge.ID = "after"
			case "policy":
				engine.bestAvailable = true
			case "audio":
				service, _ := cachedAudioService(t, cat, "last")
				session, err := service.Begin(context.Background(), poolIntent(1), cat.CatalogVersion(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				engine.audioSession = session
				if _, err := session.Check(context.Background(), candidates[0].Track, false); err != nil {
					t.Fatal(err)
				}
			}
			after := engine.assemblyKey(candidates, intent, request, references, required, waypoints, seed)
			if before == "" || after == "" || before == after {
				t.Fatalf("%s was not captured by value", change)
			}
		})
	}
}

func TestAssemblyCacheHonorsCancellationAndRejectsUnserializableInputs(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	engine.assemblyCache = &completedAssembly{}
	intent := testIntent(1)
	request := ports.RecommendationRequest{Intent: intent}
	candidates := candidatesForTracks(refs(cat, "last"))
	got, err := engine.assembleCandidates(context.Background(), candidates, intent, request, nil, nil, nil, 42)
	if err != nil || !got.complete(1) || engine.assemblyCache.key == "" {
		t.Fatalf("valid complete result not cached: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.assembleCandidates(ctx, candidates, intent, request, nil, nil, nil, 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached result bypassed cancellation: %v", err)
	}
	candidates[0].Scores.SemanticMatch = math.NaN()
	if key := engine.assemblyKey(candidates, intent, request, nil, nil, nil, 42); key != "" {
		t.Fatal("unserializable input received a reusable cache key")
	}
	before := engine.assemblyCache.key
	if a, err := engine.assembleCandidates(context.Background(), nil, intent, request, nil, nil, nil, 42); err != nil || a.complete(1) || engine.assemblyCache.key != before {
		t.Fatal("incomplete assembly replaced a valid cache entry", err)
	}
}

func BenchmarkAssemblyKey100Tracks(b *testing.B) {
	cat, sequence := benchmarkCategoryRequest()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	request := ports.RecommendationRequest{Intent: sequence.Intent}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if key := engine.assemblyKey(sequence.Candidates, sequence.Intent, request, nil, sequence.Required, sequence.Waypoints, 42); key == "" {
			b.Fatal("unserializable benchmark fixture")
		}
	}
}
