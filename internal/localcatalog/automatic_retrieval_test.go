package localcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func TestAutomaticQueryOpportunitiesAreBoundedAndDoNotMutateInput(t *testing.T) {
	intent := core.MusicIntent{References: []core.IntentReference{
		{Kind: core.ReferenceArtist, Query: "First", TrackID: "first-1", Resolution: &core.ReferenceResolution{Selected: &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "first-1"}, {TrackID: "first-2"}, {TrackID: "first-3"}}}}},
		{Kind: core.ReferenceArtist, Query: "Second", TrackID: "second-1"},
	}}
	var queries []Query
	for _, id := range []string{"first-1", "first-2", "first-3", "second-1"} {
		queries = append(queries, Query{MERT: &NeighborQuery{SeedID: id, Limit: 100}}, Query{CLAP: &NeighborQuery{SeedID: id, Limit: 100}})
	}
	queries = append(queries, Query{Metadata: &MetadataQuery{Text: "electronic", Limit: 100, Criterion: &core.MusicalCriterion{Kind: "genre", Value: "electronic", Scope: "playlist"}}}, Query{Metadata: &MetadataQuery{Text: "jazz", Limit: 100, Criterion: &core.MusicalCriterion{Kind: "genre", Value: "jazz", Scope: "journey_end"}}})
	before, err := json.Marshal(queries)
	if err != nil {
		t.Fatal(err)
	}
	got := automaticQueries(queries, intent, nil)
	if len(got) != 8 {
		t.Fatalf("query bound = %d, want 8", len(got))
	}
	var opportunity []string
	for _, q := range got {
		switch {
		case q.Metadata != nil:
			if q.Metadata.Limit > 64 {
				t.Fatalf("metadata page exceeds64: %+v", q.Metadata)
			}
			opportunity = append(opportunity, "metadata:"+q.Metadata.Criterion.Scope)
		case q.MERT != nil:
			if q.MERT.Limit > 64 {
				t.Fatalf("MERT page exceeds64: %+v", q.MERT)
			}
			opportunity = append(opportunity, "mert:"+q.MERT.SeedID)
		case q.CLAP != nil:
			if q.CLAP.Limit > 64 {
				t.Fatalf("CLAP page exceeds64: %+v", q.CLAP)
			}
			opportunity = append(opportunity, "clap:"+q.CLAP.SeedID)
		}
	}
	want := []string{"metadata:journey_end", "mert:first-1", "clap:first-1", "mert:second-1", "clap:second-1", "metadata:playlist"}
	if !reflect.DeepEqual(opportunity[:len(want)], want) {
		t.Fatalf("later anchor/family/scope lost before repeated representatives: %v", opportunity)
	}
	// Mutating the returned page limits must not mutate the request's queries.
	for _, q := range got {
		if q.Metadata != nil {
			q.Metadata.Limit = 1
		}
		if q.MERT != nil {
			q.MERT.Limit = 1
		}
		if q.CLAP != nil {
			q.CLAP.Limit = 1
		}
	}
	after, err := json.Marshal(queries)
	if err != nil || string(before) != string(after) {
		t.Fatalf("input query pointers/slice mutated: %v", err)
	}
}

type automaticTestRetriever func(context.Context, ports.RetrievalRequest) ([]core.Candidate, error)

func (f automaticTestRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	return f(ctx, request)
}

func TestAutomaticRetrievalSkipsProfilePreparation(t *testing.T) {
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "seed", Artist: "Seed Artist", Title: "Song"}}, nil)
	defer local.Close()
	executor, err := NewExecutor(local, 1)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor.metadata = func(_ context.Context, value any) ([]Hit, error) {
		query := value.(MetadataQuery)
		if query.Artist != "" || query.Text != "Seed Artist" || query.Limit > 64 {
			t.Errorf("profile query or unbounded page reached executor: %+v", query)
		}
		calls.Add(1)
		return []Hit{{Track: Track{ID: local.NamespacedID("candidate"), Artist: "Other", Title: "Complete"}, Evidence: Evidence{Channel: MetadataChannel, Rank: 1, Score: 1}}}, nil
	}
	base := automaticTestRetriever(func(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) { return nil, nil })
	retriever, err := NewCombinedRetriever(base, local, ModeCombined, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Dynamic profiles query this connection before reaching the executor. Hold
	// it to distinguish skipping that work from merely discarding its output.
	local.indexes.metadata.SetMaxOpenConns(1)
	conn, err := local.indexes.metadata.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, knowledge := range []*core.KnowledgeSnapshot{nil, {PackProfiles: []core.DiscoveryProfile{{Artist: "Unrequested profile artist"}}}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		request := ports.RetrievalRequest{Intent: core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.Automatic}, Knowledge: knowledge, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}}}}
		got, err := retriever.Retrieve(ctx, request)
		cancel()
		if err != nil || len(got) != 1 || got[0].Track.Title != "Complete" {
			t.Fatalf("Automatic waited for profile SQL or lost completed executor result: candidates=%+v err=%v", got, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected metadata query count: %d", calls.Load())
	}
}

func TestAutomaticRetrievalRetainsCompletedSourcesAndJoinsCanceledSibling(t *testing.T) {
	// One job worker makes completion ordering explicit: the first Query call
	// returns and commits its result slot before the second can start.
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, tc := range []struct {
		name     string
		mode     core.RecommendationMode
		deadline bool
	}{{"automatic canceled", core.Automatic, false}, {"automatic deadline", core.Automatic, true}, {"legacy canceled", core.EnhancedHybrid, false}} {
		t.Run(tc.name, func(t *testing.T) {
			local, _ := openTestCatalog(t, []librarypack.Track{{ID: "fixture", Artist: "First", Title: "Fixture"}}, nil)
			defer local.Close()
			executor, err := NewExecutor(local, 1)
			if err != nil {
				t.Fatal(err)
			}
			blocked, finished, baseDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			executor.metadata = func(ctx context.Context, value any) ([]Hit, error) {
				if value.(MetadataQuery).Text == "First" {
					return []Hit{{Track: Track{ID: local.NamespacedID("complete"), Artist: "Local Artist", Title: "Completed local"}, Evidence: Evidence{Channel: MetadataChannel, Rank: 1, Score: 1}}}, nil
				}
				close(blocked)
				defer close(finished)
				<-ctx.Done()
				// Even a source that returns data with an error must not publish an
				// incomplete local QueryResult as if the query completed.
				return []Hit{{Track: Track{ID: local.NamespacedID("incomplete")}}}, ctx.Err()
			}
			base := automaticTestRetriever(func(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
				defer close(baseDone)
				return []core.Candidate{{Track: core.TrackRef{ID: "base-complete", Artist: "Base Artist", Title: "Completed base"}, Sources: []core.RetrievalEvidence{{Channel: "base", Rank: 1, QueryWeight: 1}}}}, nil
			})
			retriever, err := NewCombinedRetriever(base, local, ModeCombined, 1)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			type response struct {
				candidates []core.Candidate
				err        error
			}
			done := make(chan response, 1)
			request := ports.RetrievalRequest{Intent: core.MusicIntent{Controls: core.IntentControls{RecommendationMode: tc.mode}, Knowledge: &core.KnowledgeSnapshot{}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "First", Influence: core.InfluencePositive}, {Kind: core.ReferenceArtist, Query: "Block", Influence: core.InfluencePositive}}}}
			go func() { candidates, err := retriever.Retrieve(ctx, request); done <- response{candidates, err} }()
			select {
			case <-blocked:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("second query did not begin after completed first query")
			}
			<-baseDone
			if !tc.deadline {
				cancel()
			}
			var got response
			select {
			case got = <-done:
			case <-time.After(time.Second):
				t.Fatal("canceled retrieval did not join its workers")
			}
			wantErr := context.Canceled
			if tc.deadline {
				wantErr = context.DeadlineExceeded
			}
			if !errors.Is(got.err, wantErr) {
				t.Fatalf("cancellation swallowed: %v", got.err)
			}
			select {
			case <-finished:
			default:
				t.Fatal("retrieval returned before blocked source finished")
			}
			if tc.mode == core.Automatic {
				if len(got.candidates) != 2 || !containsCandidate(got.candidates, "base-complete") || !containsCandidate(got.candidates, local.NamespacedID("complete")) || containsCandidate(got.candidates, local.NamespacedID("incomplete")) {
					t.Fatalf("completed sources lost or interrupted data admitted: %+v", got.candidates)
				}
			} else if len(got.candidates) != 0 {
				t.Fatalf("legacy cancellation contract changed: %+v", got.candidates)
			}
		})
	}
}

func TestAutomaticRetrievalAlreadyCanceledRemainsCanceled(t *testing.T) {
	local, _ := openTestCatalog(t, testTracks(), nil)
	defer local.Close()
	executor, err := NewExecutor(local, 1)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor.metadata = func(context.Context, any) ([]Hit, error) { calls.Add(1); return nil, nil }
	base := automaticTestRetriever(func(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) { return nil, ctx.Err() })
	retriever, err := NewCombinedRetriever(base, local, ModeCombined, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}}}})
	if !errors.Is(err, context.Canceled) || len(got) != 0 || calls.Load() != 0 {
		t.Fatalf("already canceled request ran or became successful: candidates=%v calls=%d err=%v", got, calls.Load(), err)
	}
}

type automaticForwardedVectorSource struct {
	ports.Catalog
	ports.LibraryAudioCatalog
	source core.LibraryEvidenceSource
	cancel context.CancelFunc
	calls  int
}

func (s *automaticForwardedVectorSource) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	s.calls++
	if s.cancel != nil {
		s.cancel()
		return core.LibraryVector{}, false, ctx.Err()
	}
	var owner, rep int
	_, _ = fmt.Sscanf(id, "base-%d-%d", &owner, &rep)
	x := float32(owner*5+rep+1) / 20
	return core.LibraryVector{Source: s.source, Values: []float32{x, float32(math.Sqrt(1 - float64(x*x))), 0}}, true, nil
}
func automaticForwardedReferences() []core.IntentReference {
	var refs []core.IntentReference
	for owner := 0; owner < 3; owner++ {
		ref := core.IntentReference{Kind: core.ReferenceArtist, Query: fmt.Sprint("Artist", owner), Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Selected: &core.ResolutionCandidate{}}}
		for rep := 0; rep < 5; rep++ {
			ref.Resolution.Selected.Representatives = append(ref.Resolution.Selected.Representatives, core.WeightedTrack{TrackID: fmt.Sprintf("base-%d-%d", owner, rep), Weight: .2})
		}
		refs = append(refs, ref)
	}
	return refs
}
func TestAutomaticRetrievalStopsForwardedVectorReadsAfterCancel(t *testing.T) {
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "fixture", Artist: "Fixture", Title: "Fixture"}}, nil)
	defer local.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &automaticForwardedVectorSource{source: local.EvidenceSource(), cancel: cancel}
	base := automaticTestRetriever(func(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) { return nil, ctx.Err() })
	r, err := NewCombinedRetriever(base, local, ModeCombined, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.catalog = source
	_, err = r.Retrieve(ctx, ports.RetrievalRequest{Intent: core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: automaticForwardedReferences()}})
	if !errors.Is(err, context.Canceled) || source.calls != 1 {
		t.Fatalf("forwarded reads=%d want1; err=%v", source.calls, err)
	}
}
func TestAutomaticRetrievalForwardedBaseVectorsRetainAllAnchorOpportunities(t *testing.T) {
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "fixture", Artist: "Fixture", Title: "Fixture"}}, nil)
	defer local.Close()
	executor, err := NewExecutor(local, 1)
	if err != nil {
		t.Fatal(err)
	}
	var metadataCalls, vectorCalls atomic.Int32
	executor.metadata = func(_ context.Context, value any) ([]Hit, error) {
		metadataCalls.Add(1)
		if q := value.(MetadataQuery); q.Limit <= 0 || q.Limit > 64 {
			return nil, fmt.Errorf("forwarded metadata page is not bounded: %+v", q)
		}
		return nil, nil
	}
	var mu sync.Mutex
	owners := map[int]bool{}
	space := local.VectorSpace()
	executor.mert = func(_ context.Context, value any) ([]Hit, error) {
		q := value.(NeighborQuery)
		vectorCalls.Add(1)
		if q.SeedID != "" || q.Space == nil || *q.Space != space || !validQueryVector(q.Vector, space.Dimension) || q.Limit <= 0 || q.Limit > 64 {
			return nil, fmt.Errorf("forwarded vector acquired local seed identity or lost space/vector/page contract: %+v", q)
		}
		mu.Lock()
		owners[(int(math.Round(float64(q.Vector[0])*20))-1)/5] = true
		mu.Unlock()
		return nil, nil
	}
	source := &automaticForwardedVectorSource{source: local.EvidenceSource()}
	base := automaticTestRetriever(func(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) { return nil, nil })
	r, err := NewCombinedRetriever(base, local, ModeCombined, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.catalog = source
	_, err = r.Retrieve(context.Background(), ports.RetrievalRequest{Intent: core.MusicIntent{Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: automaticForwardedReferences()}})
	if err != nil || len(owners) != 3 || !owners[0] || !owners[1] || !owners[2] {
		t.Fatalf("forwarded MERT anchor opportunities=%v want all3; err=%v", owners, err)
	}
	if total := metadataCalls.Load() + vectorCalls.Load(); total > 8 || metadataCalls.Load() == 0 {
		t.Fatalf("query cap or metadata opportunity lost: metadata=%d vectors=%d", metadataCalls.Load(), vectorCalls.Load())
	}
}

func TestAutomaticTextQueriesReserveEveryDescriptiveStage(t *testing.T) {
	var queries []Query
	scopes := map[*NeighborQuery]string{}
	for _, scope := range []string{"journey_start", "journey_via", "journey_end"} {
		for i := 0; i < 10; i++ {
			q := &NeighborQuery{Vector: []float32{float32(i + 1), 1}, Limit: 100}
			scopes[q] = scope
			queries = append(queries, Query{CLAP: q})
		}
	}
	got := automaticQueries(queries, core.MusicIntent{}, nil, scopes)
	if len(got) != 8 {
		t.Fatal(len(got))
	}
	// Each stage gets its first vector before any gets a second one; query
	// pointers are copied, so compare vector backing data to original scopes.
	for i := 0; i < 3; i++ {
		if got[i].CLAP.Vector[0] != 1 {
			t.Fatalf("stage starved by earlier descriptions: %+v", got)
		}
	}
	if got[3].CLAP.Vector[0] != 2 {
		t.Fatal("did not round-robin stages")
	}
}
