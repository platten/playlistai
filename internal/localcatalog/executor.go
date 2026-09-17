package localcatalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/platten/playlistai/internal/librarypack"
)

type channelRunner func(context.Context, any) ([]Hit, error)

// Executor bounds active requests and channel work across simultaneous callers.
// Each admitted request runs its requested metadata and MERT channels
// concurrently, then merges only after both complete in canonical order.
type Executor struct {
	catalog      *Catalog
	capacity     int
	requestSlots chan struct{}
	channelSlots chan struct{}
	metadata     channelRunner
	mert         channelRunner
}

// sharedQueryBudget is admission state only. Executors remain catalog-bound so
// namespaces, provenance, and root snapshots cannot leak between pins. Every
// Catalog opened on the exact same immutable Generation shares these slots.
type sharedQueryBudget struct {
	mu           sync.Mutex
	capacity     int
	requestSlots chan struct{}
	channelSlots chan struct{}
}

var queryBudgetRegistry = struct {
	sync.Mutex
	entries map[*librarypack.Generation]struct {
		budget *sharedQueryBudget
		refs   int
	}
}{entries: make(map[*librarypack.Generation]struct {
	budget *sharedQueryBudget
	refs   int
})}

func acquireQueryBudget(generation *librarypack.Generation) *sharedQueryBudget {
	queryBudgetRegistry.Lock()
	defer queryBudgetRegistry.Unlock()
	entry, ok := queryBudgetRegistry.entries[generation]
	if !ok {
		entry.budget = &sharedQueryBudget{}
	}
	entry.refs++
	queryBudgetRegistry.entries[generation] = entry
	return entry.budget
}

func releaseQueryBudget(generation *librarypack.Generation, budget *sharedQueryBudget) {
	if generation == nil || budget == nil {
		return
	}
	queryBudgetRegistry.Lock()
	defer queryBudgetRegistry.Unlock()
	entry, ok := queryBudgetRegistry.entries[generation]
	if !ok || entry.budget != budget {
		return
	}
	entry.refs--
	if entry.refs == 0 {
		delete(queryBudgetRegistry.entries, generation)
	} else {
		queryBudgetRegistry.entries[generation] = entry
	}
}

func (b *sharedQueryBudget) configure(capacity int) (chan struct{}, chan struct{}, error) {
	if b == nil {
		return nil, nil, ErrClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity == 0 {
		b.capacity = capacity
		b.requestSlots = make(chan struct{}, capacity)
		b.channelSlots = make(chan struct{}, capacity)
	} else if b.capacity != capacity {
		return nil, nil, fmt.Errorf("localcatalog: generation query budget is %d, requested %d", b.capacity, capacity)
	}
	return b.requestSlots, b.channelSlots, nil
}

// NewExecutor returns the one executor owned by catalog. Distinct catalog pins
// over the same immutable generation receive distinct adapters but share one
// admission budget. The first pin fixes the capacity until every pin releases;
// conflicting capacities are rejected rather than silently multiplying pools.
func NewExecutor(catalog *Catalog, maxConcurrent int) (*Executor, error) {
	if catalog == nil || maxConcurrent <= 0 || maxConcurrent > 1024 {
		return nil, errors.New("localcatalog: invalid executor capacity")
	}
	catalog.mu.RLock()
	if catalog.closed || catalog.generation == nil || catalog.budget == nil {
		catalog.mu.RUnlock()
		return nil, ErrClosed
	}
	catalog.executorMu.Lock()
	defer catalog.executorMu.Unlock()
	defer catalog.mu.RUnlock()
	if catalog.executor != nil {
		if catalog.executor.capacity != maxConcurrent {
			return nil, fmt.Errorf("localcatalog: catalog query budget is %d, requested %d", catalog.executor.capacity, maxConcurrent)
		}
		return catalog.executor, nil
	}
	requestSlots, channelSlots, err := catalog.budget.configure(maxConcurrent)
	if err != nil {
		return nil, err
	}
	executor := &Executor{
		catalog: catalog, capacity: maxConcurrent, requestSlots: requestSlots, channelSlots: channelSlots,
	}
	executor.metadata = func(ctx context.Context, value any) ([]Hit, error) {
		return catalog.Search(ctx, value.(MetadataQuery))
	}
	executor.mert = func(ctx context.Context, value any) ([]Hit, error) {
		return catalog.Neighbors(ctx, value.(NeighborQuery))
	}
	catalog.executor = executor
	return executor, nil
}

func (e *Executor) Query(ctx context.Context, query Query) (QueryResult, error) {
	if e == nil || e.catalog == nil {
		return QueryResult{}, errors.New("localcatalog: nil executor")
	}
	if query.Metadata == nil && query.MERT == nil {
		_, done, err := e.catalog.withGeneration()
		if err != nil {
			return QueryResult{}, err
		}
		done()
		return QueryResult{PackID: e.catalog.manifest.PackID, Candidates: []Candidate{}}, nil
	}
	select {
	case e.requestSlots <- struct{}{}:
		defer func() { <-e.requestSlots }()
	case <-ctx.Done():
		return QueryResult{}, ctx.Err()
	}
	// Keep this catalog pin open from admission through deterministic merge.
	// Close waits for admitted work; queued canceled work never acquires it.
	_, releaseCatalog, err := e.catalog.withGeneration()
	if err != nil {
		return QueryResult{}, err
	}
	defer releaseCatalog()
	type task struct {
		index  int
		runner channelRunner
		value  any
	}
	tasks := make([]task, 0, 2)
	if query.Metadata != nil {
		tasks = append(tasks, task{index: 0, runner: e.metadata, value: *query.Metadata})
	}
	if query.MERT != nil {
		tasks = append(tasks, task{index: 1, runner: e.mert, value: *query.MERT})
	}
	type completed struct {
		index int
		hits  []Hit
		err   error
	}
	results := make(chan completed, len(tasks))
	for _, item := range tasks {
		item := item
		go func() {
			select {
			case e.channelSlots <- struct{}{}:
				defer func() { <-e.channelSlots }()
			case <-ctx.Done():
				results <- completed{index: item.index, err: ctx.Err()}
				return
			}
			hits, err := item.runner(ctx, item.value)
			results <- completed{index: item.index, hits: hits, err: err}
		}()
	}
	ordered := make([][]Hit, 2)
	var joined error
	for range tasks {
		result := <-results
		ordered[result.index] = result.hits
		joined = errors.Join(joined, result.err)
	}
	if joined != nil {
		return QueryResult{}, joined
	}
	return QueryResult{PackID: e.catalog.manifest.PackID, Candidates: mergeHits(ordered)}, nil
}

func mergeHits(channels [][]Hit) []Candidate {
	byID := make(map[string]*Candidate)
	for _, hits := range channels {
		for _, hit := range hits {
			candidate := byID[hit.Track.ID]
			if candidate == nil {
				copy := Candidate{Track: hit.Track}
				candidate = &copy
				byID[hit.Track.ID] = candidate
			}
			candidate.Evidence = append(candidate.Evidence, hit.Evidence)
		}
	}
	result := make([]Candidate, 0, len(byID))
	for _, candidate := range byID {
		sort.SliceStable(candidate.Evidence, func(i, j int) bool {
			return evidenceLess(candidate.Evidence[i], candidate.Evidence[j])
		})
		result = append(result, *candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Evidence[0], result[j].Evidence[0]
		if channelOrder(left.Channel) != channelOrder(right.Channel) {
			return channelOrder(left.Channel) < channelOrder(right.Channel)
		}
		if left.Rank != right.Rank {
			return left.Rank < right.Rank
		}
		return result[i].Track.ID < result[j].Track.ID
	})
	return result
}

func evidenceLess(left, right Evidence) bool {
	if channelOrder(left.Channel) != channelOrder(right.Channel) {
		return channelOrder(left.Channel) < channelOrder(right.Channel)
	}
	if left.Rank != right.Rank {
		return left.Rank < right.Rank
	}
	if left.QueryID != right.QueryID {
		return left.QueryID < right.QueryID
	}
	return false
}

func channelOrder(channel string) int {
	switch channel {
	case MetadataChannel:
		return 0
	case MERTChannel:
		return 1
	default:
		return 2
	}
}
