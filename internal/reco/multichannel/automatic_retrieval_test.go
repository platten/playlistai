package multichannel

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type automaticRetrievalFunc func(context.Context, ports.RetrievalRequest) ([]core.Candidate, error)

func (f automaticRetrievalFunc) Retrieve(ctx context.Context, r ports.RetrievalRequest) ([]core.Candidate, error) {
	return f(ctx, r)
}

func TestAutomaticRetrievalReservesLiveBatchAndGraphOpportunity(t *testing.T) {
	catalog, intent, pool := automaticFixture(t, 2)
	automaticSupport(catalog, intent, "0", "house")
	automaticSupport(catalog, intent, "1", "house")
	var order []string
	var localDeadline, graphDeadline time.Time
	graph := automaticRetrievalFunc(func(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
		order = append(order, "graph")
		graphDeadline, _ = ctx.Deadline()
		<-ctx.Done()
		return pool.candidates[1:2], ctx.Err()
	})
	local := automaticRetrievalFunc(func(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
		order = append(order, "local")
		localDeadline, _ = ctx.Deadline()
		<-ctx.Done()
		return pool.candidates[:1], ctx.Err()
	})
	parent, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	got, err := NewAutomatic(catalog, nil, local, DefaultConfig()).WithPreparedRetriever(graph).Build(parent, intent)
	if err != nil || parent.Err() != nil || len(got.Tracks) != 2 || got.Outcome.State != core.OutcomePartial {
		t.Fatalf("completed source hits lost before live preparation: tracks=%v outcome=%+v err=%v parent=%v", got.Tracks, got.Outcome, err, parent.Err())
	}
	parentDeadline, _ := parent.Deadline()
	if !reflect.DeepEqual(order, []string{"graph", "local"}) || !graphDeadline.Before(localDeadline) || !localDeadline.Before(parentDeadline) {
		t.Fatalf("source opportunities/reserves lost: %v graph=%v local=%v parent=%v", order, graphDeadline, localDeadline, parentDeadline)
	}
	if got.Search == nil || got.Search.StopReason != "preparation_stopped" || got.Search.Considered != 2 || got.Search.Eligible != 2 || got.Search.Validate() != nil {
		t.Fatalf("partial search evidence lost: %+v", got.Search)
	}
	for _, candidate := range got.Search.Candidates {
		if len(candidate.Sources) == 0 || candidate.Sources[0].Channel != "metadata" {
			t.Fatalf("completed source provenance changed: %+v", candidate)
		}
	}
}

func TestAutomaticRetrievalParentCancellationStillAborts(t *testing.T) {
	catalog, intent, pool := automaticFixture(t, 1)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := automaticRetrievalFunc(func(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
		cancel()
		<-ctx.Done()
		return pool.candidates[:1], ctx.Err()
	})
	got, err := NewAutomatic(catalog, nil, local, DefaultConfig()).Build(parent, intent)
	if !errors.Is(err, context.Canceled) || got.Search != nil || len(got.Tracks) != 0 {
		t.Fatalf("user cancellation became a saved partial success: %+v %v", got, err)
	}
}

type automaticBoundedSearch struct {
	ports.SimilarityEngine
	budgets []int
}

func (*automaticBoundedSearch) Len() int { return 10000 }
func (s *automaticBoundedSearch) Search(_ context.Context, q ports.SimilarityQuery) ([]ports.Match, error) {
	s.budgets = append(s.budgets, q.K)
	return nil, nil
}

func TestAutomaticBaseRetrievalBoundsPagesWithoutExploration(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.Automatic, core.EnhancedHybrid, core.DeejAIOnly} {
		t.Run(string(mode), func(t *testing.T) {
			catalog, intent, _ := automaticFixture(t, 1)
			intent.Controls.RecommendationMode = mode
			intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "0", Influence: core.InfluencePositive}}
			cfg := DefaultConfig()
			cfg.SeedAudioBudget, cfg.SeedCooccurrenceBudget = 200, 200
			search := &automaticBoundedSearch{}
			_, err := NewRetriever(catalog, search, cfg).Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
			if err != nil || len(search.budgets) != 2 {
				t.Fatalf("reference queries changed: %v %v", search.budgets, err)
			}
			want := enhancedChannelBatch
			if mode == core.DeejAIOnly {
				want = 200 + cfg.ExplorationPool/2
			}
			for _, budget := range search.budgets {
				if budget != want {
					t.Fatalf("query budget=%d want=%d", budget, want)
				}
			}
		})
	}
}
