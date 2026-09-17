package librarysearch

import (
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	mmap "github.com/edsrzf/mmap-go"
)

const MaxSearchLimit = 10_000

type mappedShard struct {
	vectors mmap.MMap
	ids     []string
	invNorm []float64
	rows    int
}

type Index struct {
	path     string
	manifest Manifest
	shards   []mappedShard
	mu       sync.RWMutex
	closed   bool
	close    sync.Once
	closeErr error
}

type Query struct {
	Vector  []float32
	Limit   int
	Exclude map[string]struct{}
	Workers int
}

type Hit struct {
	ID    string
	Score float64
}

func Open(ctx context.Context, dir string) (*Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := loadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	index := &Index{path: dir, manifest: manifest}
	opened := false
	defer func() {
		if !opened {
			_ = index.Close()
		}
	}()
	lastID := ""
	for _, declared := range manifest.Shards {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := verifyArtifact(dir, declared.Vectors); err != nil {
			return nil, err
		}
		if err := verifyArtifact(dir, declared.IDs); err != nil {
			return nil, err
		}
		shard, openErr := openShard(ctx, dir, manifest.Dimension, declared)
		if openErr != nil {
			return nil, openErr
		}
		for _, id := range shard.ids {
			if id == "" || lastID != "" && id <= lastID {
				_ = shard.vectors.Unmap()
				return nil, errors.New("librarysearch: noncanonical ID mapping")
			}
			lastID = id
		}
		index.shards = append(index.shards, shard)
	}
	opened = true
	return index, nil
}

func openShard(ctx context.Context, dir string, dimension int, declared ShardManifest) (mappedShard, error) {
	var shard mappedShard
	vectorFile, err := os.Open(filepath.Join(dir, declared.Vectors.Name))
	if err != nil {
		return shard, err
	}
	mapping, err := mmap.Map(vectorFile, mmap.RDONLY, 0)
	closeErr := vectorFile.Close()
	if err != nil {
		return shard, err
	}
	if closeErr != nil {
		_ = mapping.Unmap()
		return shard, closeErr
	}
	expected := int64(headerBytes) + int64(declared.Rows)*int64(dimension)*4
	maximumIDs := int64(headerBytes) + int64(declared.Rows)*(4+4096)
	if expected != declared.Vectors.Size || declared.IDs.Size > maximumIDs || int64(len(mapping)) != expected || len(mapping) < headerBytes || string(mapping[:8]) != vectorMagic ||
		binary.LittleEndian.Uint32(mapping[8:12]) != 1 ||
		int(binary.LittleEndian.Uint32(mapping[12:16])) != dimension ||
		int(binary.LittleEndian.Uint64(mapping[16:24])) != declared.Rows {
		_ = mapping.Unmap()
		return shard, errors.New("librarysearch: invalid packed vector header or size")
	}
	ids, err := readIDs(filepath.Join(dir, declared.IDs.Name), declared.Rows)
	if err != nil {
		_ = mapping.Unmap()
		return shard, err
	}
	invNorm := make([]float64, declared.Rows)
	for row := range declared.Rows {
		if row&255 == 0 {
			if err := ctx.Err(); err != nil {
				_ = mapping.Unmap()
				return shard, err
			}
		}
		var norm float64
		offset := headerBytes + row*dimension*4
		for column := range dimension {
			bits := binary.LittleEndian.Uint32(mapping[offset+column*4 : offset+column*4+4])
			value := float64(math.Float32frombits(bits))
			if math.IsNaN(value) || math.IsInf(value, 0) {
				_ = mapping.Unmap()
				return shard, errors.New("librarysearch: nonfinite packed vector")
			}
			norm += value * value
		}
		if norm < .999 || norm > 1.001 {
			_ = mapping.Unmap()
			return shard, errors.New("librarysearch: non-normalized packed vector")
		}
		invNorm[row] = 1 / math.Sqrt(norm)
	}
	return mappedShard{vectors: mapping, ids: ids, invNorm: invNorm, rows: declared.Rows}, nil
}

func readIDs(path string, expected int) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < headerBytes || string(raw[:8]) != idMagic || binary.LittleEndian.Uint32(raw[8:12]) != 1 ||
		int(binary.LittleEndian.Uint64(raw[16:24])) != expected {
		return nil, errors.New("librarysearch: invalid packed ID header")
	}
	ids := make([]string, 0, expected)
	cursor := headerBytes
	for range expected {
		if cursor+4 > len(raw) {
			return nil, errors.New("librarysearch: truncated ID mapping")
		}
		size := int(binary.LittleEndian.Uint32(raw[cursor : cursor+4]))
		cursor += 4
		if size <= 0 || size > 4096 || cursor+size > len(raw) {
			return nil, errors.New("librarysearch: invalid ID mapping entry")
		}
		ids = append(ids, string(raw[cursor:cursor+size]))
		cursor += size
	}
	if cursor != len(raw) {
		return nil, errors.New("librarysearch: trailing ID mapping data")
	}
	return ids, nil
}

func (i *Index) Path() string { return i.path }

func (i *Index) Manifest() Manifest {
	out := i.manifest
	out.Shards = append([]ShardManifest(nil), out.Shards...)
	return out
}

func (i *Index) Close() error {
	i.close.Do(func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		var errs []error
		for index := range i.shards {
			errs = append(errs, i.shards[index].vectors.Unmap())
			i.shards[index].vectors = nil
			i.shards[index].ids = nil
			i.shards[index].invNorm = nil
		}
		i.closed = true
		i.closeErr = errors.Join(errs...)
	})
	return i.closeErr
}

// Search performs exact cosine over fixed immutable shards. Shard-local heaps
// enter one bounded coordinator heap with a total score-descending,
// stable-ID-ascending comparator, so completion order cannot affect output.
func (i *Index) Search(ctx context.Context, query Query) ([]Hit, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.closed {
		return nil, errors.New("librarysearch: index is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query.Limit <= 0 || query.Limit > MaxSearchLimit || len(query.Vector) != i.manifest.Dimension {
		return nil, errors.New("librarysearch: invalid exact-search query")
	}
	vector := append([]float32(nil), query.Vector...)
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, errors.New("librarysearch: invalid exact-search vector")
		}
		norm += float64(value) * float64(value)
	}
	if norm <= 1e-20 {
		return nil, errors.New("librarysearch: zero exact-search vector")
	}
	scale := float32(1 / math.Sqrt(norm))
	for index := range vector {
		vector[index] *= scale
	}
	workers := query.Workers
	if workers <= 0 {
		workers = 1
	}
	workers = min(workers, len(i.shards))
	type taskResult struct {
		hits []Hit
		err  error
	}
	jobs := make(chan int)
	results := make(chan taskResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shardIndex := range jobs {
				hits, err := i.searchShard(ctx, shardIndex, vector, query.Limit, query.Exclude)
				results <- taskResult{hits: hits, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for shardIndex := range i.shards {
			select {
			case jobs <- shardIndex:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	best := make(worstHeap, 0, query.Limit)
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		for _, hit := range result.hits {
			pushHit(&best, hit, query.Limit)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sortedHits(best), nil
}

func (i *Index) searchShard(ctx context.Context, shardIndex int, query []float32, limit int, exclude map[string]struct{}) ([]Hit, error) {
	shard := &i.shards[shardIndex]
	best := make(worstHeap, 0, limit)
	dimension := i.manifest.Dimension
	for row, id := range shard.ids {
		if row&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if _, skip := exclude[id]; skip {
			continue
		}
		offset := headerBytes + row*dimension*4
		var score float64
		for column, queryValue := range query {
			bits := binary.LittleEndian.Uint32(shard.vectors[offset+column*4 : offset+column*4+4])
			score += float64(queryValue) * float64(math.Float32frombits(bits))
		}
		score *= shard.invNorm[row]
		pushHit(&best, Hit{ID: id, Score: max(-1, min(1, score))}, limit)
	}
	return sortedHits(best), nil
}

type worstHeap []Hit

func (h worstHeap) Len() int      { return len(h) }
func (h worstHeap) Swap(a, b int) { h[a], h[b] = h[b], h[a] }
func (h worstHeap) Less(a, b int) bool {
	return betterHit(h[b], h[a])
}
func (h *worstHeap) Push(value any) { *h = append(*h, value.(Hit)) }
func (h *worstHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func betterHit(left, right Hit) bool {
	if left.Score != right.Score {
		return left.Score > right.Score
	}
	return left.ID < right.ID
}

func pushHit(best *worstHeap, hit Hit, limit int) {
	if len(*best) < limit {
		heap.Push(best, hit)
	} else if betterHit(hit, (*best)[0]) {
		(*best)[0] = hit
		heap.Fix(best, 0)
	}
}

func sortedHits(best worstHeap) []Hit {
	out := append([]Hit(nil), best...)
	sort.Slice(out, func(a, b int) bool { return betterHit(out[a], out[b]) })
	return out
}

func (i *Index) String() string {
	return fmt.Sprintf("%s (%d x %d)", i.manifest.Generation, i.manifest.Rows, i.manifest.Dimension)
}
