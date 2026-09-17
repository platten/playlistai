package librarylearn

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"sync"
)

const SphericalKMeansVersion = "minibatch-spherical-fixed-block/v1"

type DenseVector struct {
	ID     string
	Values []float32
}

type SphericalOptions struct {
	Clusters        int
	BatchSize       int
	LogicalBlock    int
	MaxEpochs       int
	Workers         int
	Seed            uint64
	Tolerance       float64
	MaxScratchBytes int64
	InputGeneration string
}

type SphericalModel struct {
	Version         string
	InputGeneration string
	InputDigest     string
	Seed            uint64
	Dimension       int
	Clusters        int
	Centroids       []float32
	Counts          []uint64
	Epochs          int
	Objective       float64
}

type SphericalCheckpoint struct {
	Version         string
	InputGeneration string
	InputDigest     string
	Seed            uint64
	Dimension       int
	Clusters        int
	BatchSize       int
	LogicalBlock    int
	Tolerance       float64
	Epoch           int
	Cursor          int
	Centroids       []float32
	Totals          []float64
	Counts          []uint64
	Objective       float64
	Previous        float64
	StableEpochs    int
}

type CheckpointFunc func(SphericalCheckpoint) error

type ClusterAssignment struct {
	ID               string
	Cluster          int
	Similarity       float64
	Alternative      int
	AlternativeScore float64
}

// FitSpherical performs deterministic mini-batch spherical k-means. Every
// batch is split into fixed logical blocks; workers compute block-local sums,
// then the coordinator merges them by block ID. A checkpoint describes only a
// completed batch boundary.
func FitSpherical(ctx context.Context, input []DenseVector, options SphericalOptions, checkpoint *SphericalCheckpoint, persist CheckpointFunc) (SphericalModel, error) {
	vectors, dimension, digest, err := canonicalVectors(input)
	if err != nil {
		return SphericalModel{}, err
	}
	if err := normalizeSphericalOptions(&options, len(vectors)); err != nil {
		return SphericalModel{}, err
	}
	if len(vectors) == 0 || dimension == 0 {
		return SphericalModel{}, errors.New("librarylearn: spherical k-means requires vectors")
	}
	state := SphericalCheckpoint{
		Version: SphericalKMeansVersion, InputGeneration: options.InputGeneration,
		InputDigest: digest, Seed: options.Seed, Dimension: dimension, Clusters: options.Clusters,
		BatchSize: options.BatchSize, LogicalBlock: options.LogicalBlock, Tolerance: options.Tolerance,
		Previous: math.Inf(1),
	}
	if checkpoint == nil {
		state.Centroids = initializeSpherical(vectors, options.Clusters, options.Seed, dimension)
		state.Totals = make([]float64, options.Clusters*dimension)
		state.Counts = make([]uint64, options.Clusters)
	} else {
		state = cloneCheckpoint(*checkpoint)
		if err := validateCheckpoint(state, options, digest, dimension, len(vectors)); err != nil {
			return SphericalModel{}, err
		}
	}
	for state.Epoch < options.MaxEpochs {
		if err := ctx.Err(); err != nil {
			return SphericalModel{}, err
		}
		end := min(len(vectors), state.Cursor+options.BatchSize)
		partials, err := assignBatch(ctx, vectors[state.Cursor:end], state.Centroids, dimension, options)
		if err != nil {
			return SphericalModel{}, err
		}
		for block := range partials {
			partial := partials[block]
			for i, value := range partial.sums {
				state.Totals[i] += value
			}
			for i, count := range partial.counts {
				state.Counts[i] += count
			}
			state.Objective += partial.objective
		}
		for cluster := range options.Clusters {
			if state.Counts[cluster] == 0 {
				continue
			}
			normalizeCentroid(state.Centroids[cluster*dimension:(cluster+1)*dimension], state.Totals[cluster*dimension:(cluster+1)*dimension])
		}
		state.Cursor = end
		if state.Cursor == len(vectors) {
			empty := reinitializeEmpty(vectors, &state)
			objective := state.Objective / float64(len(vectors))
			if !empty && !math.IsInf(state.Previous, 0) && math.Abs(state.Previous-objective) <= options.Tolerance*max(1, math.Abs(state.Previous)) {
				state.StableEpochs++
			} else {
				state.StableEpochs = 0
			}
			state.Previous = objective
			state.Epoch++
			state.Cursor = 0
			state.Objective = 0
			clear(state.Totals)
			clear(state.Counts)
		}
		if persist != nil {
			if err := persist(cloneCheckpoint(state)); err != nil {
				return SphericalModel{}, err
			}
		}
		if state.Cursor == 0 && state.StableEpochs >= 2 {
			break
		}
	}
	assignments, err := AssignSpherical(ctx, vectors, SphericalModel{Centroids: state.Centroids, Dimension: dimension, Clusters: options.Clusters}, options.Workers, options.LogicalBlock)
	if err != nil {
		return SphericalModel{}, err
	}
	counts := make([]uint64, options.Clusters)
	var objective float64
	for _, assignment := range assignments {
		counts[assignment.Cluster]++
		objective += 1 - assignment.Similarity
	}
	return SphericalModel{
		Version: SphericalKMeansVersion, InputGeneration: options.InputGeneration, InputDigest: digest,
		Seed: options.Seed, Dimension: dimension, Clusters: options.Clusters,
		Centroids: append([]float32(nil), state.Centroids...), Counts: counts,
		Epochs: state.Epoch, Objective: objective / float64(len(assignments)),
	}, nil
}

func normalizeSphericalOptions(options *SphericalOptions, vectors int) error {
	if options.Clusters == 0 {
		options.Clusters = RecommendedClusters(vectors)
	}
	if options.Clusters <= 0 || options.Clusters > vectors {
		return errors.New("librarylearn: cluster count must be positive and no larger than the training sample")
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 512
	}
	if options.LogicalBlock <= 0 {
		options.LogicalBlock = 64
	}
	if options.MaxEpochs <= 0 {
		options.MaxEpochs = 30
	}
	if options.Workers <= 0 {
		options.Workers = 1
	}
	if options.Tolerance <= 0 {
		options.Tolerance = 1e-6
	}
	if options.MaxScratchBytes <= 0 {
		options.MaxScratchBytes = 128 << 20
	}
	return nil
}

// RecommendedClusters is a conservative recorded heuristic, not an optimum:
// floor(sqrt(sample/2)), capped at 256. Fewer than four vectors are explicitly
// insufficient for fitting acoustic neighborhoods.
func RecommendedClusters(vectors int) int {
	if vectors < 4 {
		return 0
	}
	return min(256, max(2, int(math.Sqrt(float64(vectors)/2))))
}

func canonicalVectors(input []DenseVector) ([]DenseVector, int, string, error) {
	vectors := make([]DenseVector, len(input))
	copy(vectors, input)
	sort.Slice(vectors, func(i, j int) bool { return vectors[i].ID < vectors[j].ID })
	dimension := 0
	h := sha256.New()
	for i := range vectors {
		if vectors[i].ID == "" || i > 0 && vectors[i].ID == vectors[i-1].ID {
			return nil, 0, "", errors.New("librarylearn: vectors require unique stable IDs")
		}
		if dimension == 0 {
			dimension = len(vectors[i].Values)
		}
		if len(vectors[i].Values) != dimension || dimension == 0 {
			return nil, 0, "", errors.New("librarylearn: inconsistent vector dimensions")
		}
		vectors[i].Values = append([]float32(nil), vectors[i].Values...)
		var normSquared float64
		for _, value := range vectors[i].Values {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, 0, "", errors.New("librarylearn: nonfinite vector")
			}
			normSquared += float64(value) * float64(value)
		}
		if normSquared <= 1e-20 {
			return nil, 0, "", errors.New("librarylearn: zero vector")
		}
		scale := float32(1 / math.Sqrt(normSquared))
		_, _ = h.Write([]byte(vectors[i].ID))
		_, _ = h.Write([]byte{0})
		var raw [4]byte
		for j := range vectors[i].Values {
			vectors[i].Values[j] *= scale
			binary.LittleEndian.PutUint32(raw[:], math.Float32bits(vectors[i].Values[j]))
			_, _ = h.Write(raw[:])
		}
	}
	return vectors, dimension, stringHex(h.Sum(nil)), nil
}

func initializeSpherical(vectors []DenseVector, clusters int, seed uint64, dimension int) []float32 {
	centroids := make([]float32, clusters*dimension)
	selected := make([]bool, len(vectors))
	first := 0
	for i := 1; i < len(vectors); i++ {
		if keyedLess(seed, "kmeans-first", vectors[i].ID, vectors[first].ID) {
			first = i
		}
	}
	copy(centroids[:dimension], vectors[first].Values)
	selected[first] = true
	for cluster := 1; cluster < clusters; cluster++ {
		weights := make([]float64, len(vectors))
		var total float64
		for i, vector := range vectors {
			if selected[i] {
				continue
			}
			best := -1.0
			for existing := 0; existing < cluster; existing++ {
				best = max(best, dot32(vector.Values, centroids[existing*dimension:(existing+1)*dimension]))
			}
			distance := max(0, 1-best)
			weights[i] = distance * distance
			total += weights[i]
		}
		chosen := -1
		if total > 0 {
			threshold := keyedFloat(seed, cluster) * total
			var cumulative float64
			for i := range vectors {
				cumulative += weights[i]
				if !selected[i] && cumulative >= threshold {
					chosen = i
					break
				}
			}
		}
		if chosen < 0 {
			for i := range vectors {
				if !selected[i] {
					chosen = i
					break
				}
			}
		}
		selected[chosen] = true
		copy(centroids[cluster*dimension:(cluster+1)*dimension], vectors[chosen].Values)
	}
	return centroids
}

type batchPartial struct {
	index     int
	sums      []float64
	counts    []uint64
	objective float64
	err       error
}

func assignBatch(ctx context.Context, vectors []DenseVector, centroids []float32, dimension int, options SphericalOptions) ([]batchPartial, error) {
	blocks := (len(vectors) + options.LogicalBlock - 1) / options.LogicalBlock
	bytesNeeded := int64(blocks) * int64(options.Clusters*dimension*8+options.Clusters*8)
	if bytesNeeded > options.MaxScratchBytes {
		return nil, errors.New("librarylearn: k-means logical-block scratch exceeds budget")
	}
	results := make([]batchPartial, blocks)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(options.Workers, max(1, blocks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for block := range jobs {
				partial := batchPartial{index: block, sums: make([]float64, options.Clusters*dimension), counts: make([]uint64, options.Clusters)}
				start, end := block*options.LogicalBlock, min(len(vectors), (block+1)*options.LogicalBlock)
				for i := start; i < end; i++ {
					if err := ctx.Err(); err != nil {
						partial.err = err
						break
					}
					cluster, score, _, _ := nearest(vectors[i].Values, centroids, dimension, options.Clusters)
					partial.counts[cluster]++
					partial.objective += 1 - score
					base := cluster * dimension
					for j, value := range vectors[i].Values {
						partial.sums[base+j] += float64(value)
					}
				}
				results[block] = partial
			}
		}()
	}
	func() {
		defer close(jobs)
		for block := range blocks {
			select {
			case jobs <- block:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func reinitializeEmpty(vectors []DenseVector, state *SphericalCheckpoint) bool {
	empty := false
	used := map[int]bool{}
	for cluster, count := range state.Counts {
		if count != 0 {
			continue
		}
		empty = true
		chosen, worst := -1, -1.0
		for i, vector := range vectors {
			if used[i] {
				continue
			}
			_, score, _, _ := nearest(vector.Values, state.Centroids, state.Dimension, state.Clusters)
			distance := 1 - score
			if distance > worst || distance == worst && (chosen < 0 || vector.ID < vectors[chosen].ID) {
				chosen, worst = i, distance
			}
		}
		if chosen >= 0 {
			copy(state.Centroids[cluster*state.Dimension:(cluster+1)*state.Dimension], vectors[chosen].Values)
			used[chosen] = true
		}
	}
	return empty
}

func normalizeCentroid(destination []float32, source []float64) {
	var squared float64
	for _, value := range source {
		squared += value * value
	}
	if squared <= 1e-30 {
		return
	}
	scale := 1 / math.Sqrt(squared)
	for i, value := range source {
		destination[i] = float32(value * scale)
	}
}

func nearest(vector, centroids []float32, dimension, clusters int) (best int, score float64, alternative int, alternativeScore float64) {
	best, alternative = -1, -1
	score, alternativeScore = -math.MaxFloat64, -math.MaxFloat64
	for cluster := range clusters {
		value := dot32(vector, centroids[cluster*dimension:(cluster+1)*dimension])
		if value > score || value == score && (best < 0 || cluster < best) {
			alternative, alternativeScore = best, score
			best, score = cluster, value
		} else if value > alternativeScore || value == alternativeScore && (alternative < 0 || cluster < alternative) {
			alternative, alternativeScore = cluster, value
		}
	}
	return best, score, alternative, alternativeScore
}

func dot32(left, right []float32) float64 {
	var result float64
	for i, value := range left {
		result += float64(value) * float64(right[i])
	}
	return result
}

func keyedFloat(seed uint64, step int) float64 {
	digest := keyedPriority(seed, "kmeans-step", string(binary.LittleEndian.AppendUint64(nil, uint64(step))))
	return float64(binary.LittleEndian.Uint64(digest[:8])>>11) / float64(uint64(1)<<53)
}

func validateCheckpoint(state SphericalCheckpoint, options SphericalOptions, digest string, dimension, vectors int) error {
	if state.Version != SphericalKMeansVersion || state.InputGeneration != options.InputGeneration || state.InputDigest != digest ||
		state.Seed != options.Seed || state.Dimension != dimension || state.Clusters != options.Clusters ||
		state.BatchSize != options.BatchSize || state.LogicalBlock != options.LogicalBlock || state.Tolerance != options.Tolerance ||
		state.Epoch < 0 || state.Epoch > options.MaxEpochs || state.Cursor < 0 || state.Cursor > vectors ||
		len(state.Centroids) != options.Clusters*dimension || len(state.Totals) != options.Clusters*dimension || len(state.Counts) != options.Clusters {
		return errors.New("librarylearn: incompatible spherical k-means checkpoint")
	}
	return nil
}

func cloneCheckpoint(source SphericalCheckpoint) SphericalCheckpoint {
	source.Centroids = append([]float32(nil), source.Centroids...)
	source.Totals = append([]float64(nil), source.Totals...)
	source.Counts = append([]uint64(nil), source.Counts...)
	return source
}

func stringHex(raw []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, value := range raw {
		out[2*i], out[2*i+1] = digits[value>>4], digits[value&15]
	}
	return string(out)
}

// AssignSpherical assigns canonical vectors to frozen centroids using fixed
// logical blocks. Returned rows are stable-ID ordered for all worker counts.
func AssignSpherical(ctx context.Context, input []DenseVector, model SphericalModel, workers, logicalBlock int) ([]ClusterAssignment, error) {
	vectors, dimension, _, err := canonicalVectors(input)
	if err != nil {
		return nil, err
	}
	if dimension != model.Dimension || model.Clusters <= 0 || len(model.Centroids) != model.Clusters*dimension {
		return nil, errors.New("librarylearn: incompatible spherical assignment model")
	}
	workers = max(1, workers)
	logicalBlock = max(1, logicalBlock)
	blocks := (len(vectors) + logicalBlock - 1) / logicalBlock
	result := make([]ClusterAssignment, len(vectors))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, max(1, blocks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for block := range jobs {
				start, end := block*logicalBlock, min(len(vectors), (block+1)*logicalBlock)
				for i := start; i < end; i++ {
					if ctx.Err() != nil {
						return
					}
					cluster, score, alternative, alternativeScore := nearest(vectors[i].Values, model.Centroids, dimension, model.Clusters)
					result[i] = ClusterAssignment{ID: vectors[i].ID, Cluster: cluster, Similarity: score, Alternative: alternative, AlternativeScore: alternativeScore}
				}
			}
		}()
	}
	func() {
		defer close(jobs)
		for block := range blocks {
			select {
			case jobs <- block:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
