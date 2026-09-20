package localcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
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
	generation, releaseCatalog, err := e.catalog.withGeneration()
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
			queryID := independentQueryID(item.value)
			if query, ok := item.value.(NeighborQuery); ok && query.SeedID != "" && err == nil {
				// File aliases of one reference recording are one retrieval
				// query, not independent votes for every neighbor they share.
				if id, idErr := e.catalog.localID(query.SeedID); idErr == nil {
					if track, found, lookupErr := generation.Lookup(ctx, id); lookupErr != nil {
						err = lookupErr
					} else if found {
						identity := librarypack.RecordingIdentity(packTrack(e.catalog.convertTrack(track)))
						if identity != "" {
							queryID = "seed-recording:" + identity
						}
					}
				}
			}
			for i := range hits {
				hits[i].Evidence.QueryID = queryID
			}
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
	sort.Slice(result, func(i, j int) bool { return candidateLess(result[i], result[j]) })
	result = deduplicateCandidates(result)
	// File copies must not push unrelated recordings down a query's ranking.
	// This runs on each complete bounded query result, not on the later union
	// of queries where absent ranks can represent legitimately missing hits.
	compactRecordingRanks(result)
	return result
}

func independentQueryID(value any) string {
	switch query := value.(type) {
	case MetadataQuery:
		text := normalizeUnicode(query.Text)
		if query.Criterion != nil {
			return "criterion:" + normalizeUnicode(query.Criterion.Kind) + ":" + normalizeUnicode(query.Criterion.Value) + ":" + text
		}
		return text
	case NeighborQuery:
		if query.SeedID != "" {
			return query.SeedID
		}
		data := make([]byte, 4*len(query.Vector))
		for i, value := range query.Vector {
			binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
		}
		return fmt.Sprintf("vector:%x", sha256.Sum256(data))
	default:
		return ""
	}
}

func evidenceIdentity(e Evidence) string {
	// Neither score nor rank identifies an independent observation. Retain the
	// full provenance and representation contract, including model revisions.
	key, _ := json.Marshal(struct {
		Channel, Query string
		Provenance     Provenance
		Space          *librarypack.VectorSpace
	}{e.Channel, e.QueryID, e.Provenance, e.VectorSpace})
	return string(key)
}

func uniqueEvidence(input []Evidence) []Evidence {
	best := make(map[string]Evidence, len(input))
	for _, evidence := range input {
		key := evidenceIdentity(evidence)
		previous, exists := best[key]
		if !exists || max(1, evidence.Rank) < max(1, previous.Rank) ||
			(max(1, evidence.Rank) == max(1, previous.Rank) && evidence.Score > previous.Score) {
			best[key] = evidence
		}
	}
	out := make([]Evidence, 0, len(best))
	for _, evidence := range best {
		out = append(out, evidence)
	}
	sort.Slice(out, func(i, j int) bool { return evidenceLess(out[i], out[j]) })
	return out
}

func compactRecordingRanks(candidates []Candidate) {
	groups := map[string][]*Evidence{}
	for i := range candidates {
		for j := range candidates[i].Evidence {
			evidence := &candidates[i].Evidence[j]
			key := evidenceIdentity(*evidence)
			groups[key] = append(groups[key], evidence)
		}
	}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool { return group[i].Rank < group[j].Rank })
		for i, evidence := range group {
			evidence.Rank = i + 1
		}
	}
}

func candidateLess(left, right Candidate) bool {
	leftEvidence, rightEvidence := left.Evidence[0], right.Evidence[0]
	if channelOrder(leftEvidence.Channel) != channelOrder(rightEvidence.Channel) {
		return channelOrder(leftEvidence.Channel) < channelOrder(rightEvidence.Channel)
	}
	if leftEvidence.Rank != rightEvidence.Rank {
		return leftEvidence.Rank < rightEvidence.Rank
	}
	return left.Track.ID < right.Track.ID
}

func deduplicateCandidates(input []Candidate) []Candidate {
	parents := make([]int, len(input))
	for index := range parents {
		parents[index] = index
	}
	var find func(int) int
	find = func(index int) int {
		if parents[index] != index {
			parents[index] = find(parents[index])
		}
		return parents[index]
	}
	join := func(left, right int) {
		left, right = find(left), find(right)
		if left == right {
			return
		}
		if left < right {
			parents[right] = left
		} else {
			parents[left] = right
		}
	}
	identifierBuckets := map[string][]int{}
	fingerprintBuckets := map[string][]int{}
	for index := range input {
		track := packTrack(input[index].Track)
		isrc := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(track.ISRC)))
		mbid := strings.ToLower(strings.TrimSpace(track.MusicBrainzRecording))
		acoustID := strings.ToLower(strings.TrimSpace(track.AcoustID))
		keys := make([]string, 0, 3)
		if isrc != "" {
			keys = append(keys, "isrc:"+isrc)
		}
		if mbid != "" {
			keys = append(keys, "mbid:"+mbid)
		}
		if acoustID != "" {
			keys = append(keys, "acoustid:"+acoustID)
		}
		for _, key := range keys {
			for _, other := range identifierBuckets[key] {
				if librarypack.SameRecording(track, packTrack(input[other].Track)) {
					join(index, other)
				}
			}
			identifierBuckets[key] = append(identifierBuckets[key], index)
		}
		if fingerprint := track.AudioFingerprint; fingerprint != nil && fingerprint.Fingerprint != "" {
			key := fingerprint.Contract + "\x00" + fingerprint.FingerprintSHA256 + "\x00" + fingerprint.Fingerprint
			for _, other := range fingerprintBuckets[key] {
				if librarypack.SameRecording(track, packTrack(input[other].Track)) {
					join(index, other)
				}
			}
			fingerprintBuckets[key] = append(fingerprintBuckets[key], index)
		}
	}
	output := make([]Candidate, 0, len(input))
	positions := map[int]int{}
	for index := range input {
		root := find(index)
		position, exists := positions[root]
		if !exists {
			copy := input[index]
			copy.Evidence = append([]Evidence(nil), input[index].Evidence...)
			positions[root] = len(output)
			output = append(output, copy)
			continue
		}
		output[position].Evidence = append(output[position].Evidence, input[index].Evidence...)
	}
	for i := range output {
		output[i].Evidence = uniqueEvidence(output[i].Evidence)
	}
	return output
}

func packTrack(track Track) librarypack.Track {
	result := librarypack.Track{
		Artist: track.Artist, Title: track.Title,
		NormalizedArtist: track.NormalizedArtist, NormalizedTitle: track.NormalizedTitle,
		ISRC: track.ISRC, MusicBrainzRecording: track.MusicBrainzRecording, AcoustID: track.AcoustID,
		AudioFingerprint: track.AudioFingerprint, DurationMilliseconds: track.DurationMilliseconds,
		DurationReliable: track.DurationReliable,
	}
	identity := strings.ToLower(strings.TrimSpace(track.RecordingIdentity))
	if value, ok := strings.CutPrefix(identity, "isrc:"); ok && result.ISRC == "" {
		result.ISRC = value
	}
	if value, ok := strings.CutPrefix(identity, "musicbrainz:"); ok && result.MusicBrainzRecording == "" {
		result.MusicBrainzRecording = value
	}
	if value, ok := strings.CutPrefix(identity, "acoustid-id:"); ok && result.AcoustID == "" {
		result.AcoustID = value
	}
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
	return evidenceIdentity(left) < evidenceIdentity(right)
}

func channelOrder(channel string) int {
	switch channel {
	case "required_local":
		return -1
	case MetadataChannel:
		return 0
	case MERTChannel:
		return 1
	default:
		return 2
	}
}
