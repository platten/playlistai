package librarysearch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type VectorRow struct {
	ID     string
	Vector []float32
}

// RowSource supplies rows in strictly increasing stable-ID order. A nil/empty
// vector records missing evidence and is omitted from the packed index.
type RowSource interface {
	Next(context.Context) (VectorRow, bool, error)
}

type SliceSource struct {
	Rows  []VectorRow
	index int
}

func (s *SliceSource) Next(ctx context.Context) (VectorRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return VectorRow{}, false, err
	}
	if s.index >= len(s.Rows) {
		return VectorRow{}, false, nil
	}
	row := s.Rows[s.index]
	s.index++
	return row, true, nil
}

type BuildOptions struct {
	Root             string
	SourceGeneration string
	Contract         string
	Dimension        int
	ShardRows        int
	Workers          int
	MaxScratchBytes  int64
}

type shardJob struct {
	index int
	rows  []VectorRow
}

type shardResult struct {
	manifest ShardManifest
	err      error
}

func Build(ctx context.Context, source RowSource, options BuildOptions) (string, Manifest, error) {
	var empty Manifest
	if source == nil || options.Root == "" || options.SourceGeneration == "" || options.Contract == "" ||
		options.Dimension <= 0 || options.Dimension > 8192 {
		return "", empty, errors.New("librarysearch: invalid build options")
	}
	if options.ShardRows <= 0 {
		options.ShardRows = 16_384
	}
	if options.Workers <= 0 {
		options.Workers = 1
	}
	if options.MaxScratchBytes <= 0 {
		options.MaxScratchBytes = 256 << 20
	}
	perWorker := int64(options.ShardRows) * int64(options.Dimension*4+64)
	if perWorker <= 0 || perWorker > options.MaxScratchBytes/2 {
		return "", empty, errors.New("librarysearch: vector shard plus source buffer exceeds scratch budget")
	}
	options.Workers = min(options.Workers, max(1, int(options.MaxScratchBytes/perWorker)-1))
	if err := os.MkdirAll(options.Root, 0o700); err != nil {
		return "", empty, err
	}
	stage, err := os.MkdirTemp(options.Root, ".vector-build-")
	if err != nil {
		return "", empty, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan shardJob)
	results := make(chan shardResult, options.Workers)
	var wg sync.WaitGroup
	for range options.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				manifest, err := writeShard(ctx, stage, options.Dimension, job)
				select {
				case results <- shardResult{manifest: manifest, err: err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	var shards []ShardManifest
	var resultErr error
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for result := range results {
			if result.err != nil && resultErr == nil {
				resultErr = result.err
				cancel()
			}
			if result.err == nil {
				shards = append(shards, result.manifest)
			}
		}
	}()
	var sourceErr error
	var lastID string
	var batch []VectorRow
	shardIndex := 0
	dispatch := func() bool {
		if len(batch) == 0 {
			return true
		}
		owned := batch
		batch = nil
		select {
		case jobs <- shardJob{index: shardIndex, rows: owned}:
			shardIndex++
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		row, ok, nextErr := source.Next(ctx)
		if nextErr != nil {
			sourceErr = nextErr
			cancel()
			break
		}
		if !ok {
			break
		}
		row.ID = strings.TrimSpace(row.ID)
		if row.ID == "" || lastID != "" && row.ID <= lastID {
			sourceErr = errors.New("librarysearch: source IDs must be unique and strictly increasing")
			cancel()
			break
		}
		lastID = row.ID
		if len(row.Vector) == 0 {
			continue
		}
		if err := validateVector(row.Vector, options.Dimension); err != nil {
			sourceErr = fmt.Errorf("librarysearch: vector %s: %w", row.ID, err)
			cancel()
			break
		}
		row.Vector = append([]float32(nil), row.Vector...)
		batch = append(batch, row)
		if len(batch) == options.ShardRows && !dispatch() {
			break
		}
	}
	if sourceErr == nil && ctx.Err() == nil {
		_ = dispatch()
	}
	close(jobs)
	<-collected
	if resultErr != nil {
		sourceErr = resultErr
	}
	if sourceErr != nil {
		return "", empty, sourceErr
	}
	if err := ctx.Err(); err != nil {
		return "", empty, err
	}
	if len(shards) == 0 {
		return "", empty, errors.New("librarysearch: source contains no valid vectors")
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].Index < shards[j].Index })
	var rows int64
	for i := range shards {
		shards[i].RowStart = rows
		rows += int64(shards[i].Rows)
	}
	identity := struct {
		Format, SourceGeneration, Contract string
		Dimension                          int
		Rows                               int64
		Shards                             []ShardManifest
	}{FormatVersion, options.SourceGeneration, options.Contract, options.Dimension, rows, shards}
	canonical, err := json.Marshal(identity)
	if err != nil {
		return "", empty, err
	}
	digest := sha256.Sum256(canonical)
	generation := "gen-" + hex.EncodeToString(digest[:12])
	manifest := Manifest{
		Format: FormatVersion, Generation: generation, SourceGeneration: options.SourceGeneration,
		Contract: options.Contract, Dimension: options.Dimension, Rows: rows, Shards: shards,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", empty, err
	}
	if err := writeSynced(filepath.Join(stage, "manifest.json"), append(raw, '\n')); err != nil {
		return "", empty, err
	}
	if err := syncDirectory(stage); err != nil {
		return "", empty, err
	}
	target := filepath.Join(options.Root, generation)
	if err := os.Rename(stage, target); err != nil {
		if existing, openErr := Open(ctx, target); openErr == nil && existing.manifest.Generation == generation {
			_ = existing.Close()
			return target, manifest, nil
		}
		return "", empty, err
	}
	keep = true
	if err := syncDirectory(options.Root); err != nil {
		return "", empty, err
	}
	return target, manifest, nil
}

func validateVector(vector []float32, dimension int) error {
	if len(vector) != dimension {
		return errors.New("dimension mismatch")
	}
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("nonfinite value")
		}
		norm += float64(value) * float64(value)
	}
	if norm < .999 || norm > 1.001 {
		return fmt.Errorf("vector norm %g is outside normalized tolerance", math.Sqrt(norm))
	}
	return nil
}

func writeShard(ctx context.Context, stage string, dimension int, job shardJob) (ShardManifest, error) {
	var manifest ShardManifest
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	vectorName := fmt.Sprintf("vectors-%06d.f32", job.index)
	idName := fmt.Sprintf("ids-%06d.ids", job.index)
	vectorPath, idPath := filepath.Join(stage, vectorName), filepath.Join(stage, idName)
	vectorFile, err := os.OpenFile(vectorPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return manifest, err
	}
	vectorHash := sha256.New()
	vectorWriter := io.MultiWriter(vectorFile, vectorHash)
	header := make([]byte, headerBytes)
	copy(header[:8], vectorMagic)
	binary.LittleEndian.PutUint32(header[8:12], 1)
	binary.LittleEndian.PutUint32(header[12:16], uint32(dimension))
	binary.LittleEndian.PutUint64(header[16:24], uint64(len(job.rows)))
	if _, err = vectorWriter.Write(header); err == nil {
		var raw [4]byte
		for _, row := range job.rows {
			if err = ctx.Err(); err != nil {
				break
			}
			for _, value := range row.Vector {
				binary.LittleEndian.PutUint32(raw[:], math.Float32bits(value))
				if _, err = vectorWriter.Write(raw[:]); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = vectorFile.Sync()
	}
	err = errors.Join(err, vectorFile.Close())
	if err != nil {
		return manifest, err
	}
	idFile, err := os.OpenFile(idPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return manifest, err
	}
	idHash := sha256.New()
	idWriter := io.MultiWriter(idFile, idHash)
	clear(header)
	copy(header[:8], idMagic)
	binary.LittleEndian.PutUint32(header[8:12], 1)
	binary.LittleEndian.PutUint64(header[16:24], uint64(len(job.rows)))
	if _, err = idWriter.Write(header); err == nil {
		var size [4]byte
		for _, row := range job.rows {
			binary.LittleEndian.PutUint32(size[:], uint32(len(row.ID)))
			if _, err = idWriter.Write(size[:]); err == nil {
				_, err = io.WriteString(idWriter, row.ID)
			}
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = idFile.Sync()
	}
	err = errors.Join(err, idFile.Close())
	if err != nil {
		return manifest, err
	}
	vectorInfo, statErr := os.Stat(vectorPath)
	if statErr != nil {
		return manifest, statErr
	}
	idInfo, statErr := os.Stat(idPath)
	if statErr != nil {
		return manifest, statErr
	}
	return ShardManifest{
		Index: job.index, Rows: len(job.rows),
		Vectors: Artifact{Name: vectorName, Size: vectorInfo.Size(), SHA256: hex.EncodeToString(vectorHash.Sum(nil))},
		IDs:     Artifact{Name: idName, Size: idInfo.Size(), SHA256: hex.EncodeToString(idHash.Sum(nil))},
	}, nil
}
