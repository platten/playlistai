package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/librarysearch"
)

type scaleBenchmarkResult struct {
	Kind                  string        `json:"kind"`
	Rows                  int           `json:"rows"`
	Dimension             int           `json:"dimension"`
	DType                 string        `json:"dtype"`
	CorpusComposition     string        `json:"corpusComposition"`
	Workers               int           `json:"workers"`
	MaxRAMBytes           int64         `json:"maxRamBytes"`
	PeakOwnedRSSBytes     int64         `json:"peakOwnedRssBytes"`
	RAMTargetMet          bool          `json:"ramTargetMet"`
	IndexBuild            time.Duration `json:"indexBuild"`
	IndexBytes            int64         `json:"indexBytes"`
	PackExport            time.Duration `json:"packExport"`
	PackBytes             int64         `json:"packBytes"`
	PackBytesPerTrack     float64       `json:"packBytesPerTrack"`
	QuerySamples          int           `json:"querySamples"`
	QueryP50              time.Duration `json:"queryP50"`
	QueryP95              time.Duration `json:"queryP95"`
	EstimatedPayloadBytes int64         `json:"estimatedUncompressedVectorBytes"`
}

func runScaleBenchmark(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer bench scale", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rows := flags.Int("rows", 2_000_000, "synthetic track/vector rows")
	dimension := flags.Int("dimension", 768, "normalized float32 vector dimension (multiple of four)")
	workers := flags.Int("workers", runtime.GOMAXPROCS(0), "index construction workers")
	queries := flags.Int("queries", 50, "exact-search latency samples")
	maxRAMText := flags.String("max-ram", "4GiB", "RSS gate and scratch target")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	maxRAM, err := parseSize(*maxRAMText)
	if err != nil {
		return 1, err
	}
	if *rows <= 0 || *rows > librarypack.DefaultLimits().MaxTracks || *dimension < 4 || *dimension > 4096 || *dimension%4 != 0 || *workers <= 0 || *queries <= 0 || *queries > 10_000 {
		return 1, errors.New("bench scale requires positive bounded rows/workers/queries and a dimension divisible by four")
	}
	scratch, err := os.MkdirTemp("", "playlist-indexer-scale-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(scratch)

	monitorDone := make(chan struct{})
	peakRSS := make(chan int64, 1)
	go monitorProcessRSS(monitorDone, peakRSS)
	result := scaleBenchmarkResult{
		Kind: "synthetic-scale", Rows: *rows, Dimension: *dimension, DType: "float32",
		CorpusComposition: "deterministic synthetic metadata with four-sparse normalized vectors; not decoded audio or a musical-quality corpus",
		Workers:           *workers, MaxRAMBytes: maxRAM, QuerySamples: *queries,
		EstimatedPayloadBytes: int64(*rows) * int64(*dimension) * 4,
	}
	finishMonitor := func() {
		close(monitorDone)
		result.PeakOwnedRSSBytes = <-peakRSS
		result.RAMTargetMet = result.PeakOwnedRSSBytes <= maxRAM
	}

	indexRoot := filepath.Join(scratch, "index")
	perRow := int64(*dimension*4 + 64)
	indexScratch := max(int64(64<<20), maxRAM/2)
	shardRows := min(16_384, max(1, int(indexScratch/int64(*workers+2)/perRow)))
	indexStart := time.Now()
	indexDir, _, err := librarysearch.Build(ctx, &syntheticScaleSource{rows: *rows, dimension: *dimension}, librarysearch.BuildOptions{
		Root: indexRoot, SourceGeneration: "synthetic-scale-v1", Contract: fmt.Sprintf("synthetic-f32-%d-v1", *dimension),
		Dimension: *dimension, ShardRows: shardRows, Workers: *workers, MaxScratchBytes: indexScratch,
	})
	result.IndexBuild = time.Since(indexStart)
	if err != nil {
		finishMonitor()
		return 1, err
	}
	if gracefulStopRequested(ctx) {
		finishMonitor()
		return 130, libraryindex.ErrShutdownRequested
	}
	result.IndexBytes, err = directoryBytes(indexDir)
	if err != nil {
		finishMonitor()
		return 1, err
	}

	index, err := librarysearch.Open(ctx, indexDir)
	if err != nil {
		finishMonitor()
		return 1, err
	}
	queryVector := syntheticVector(0, *dimension)
	latencies := make([]time.Duration, 0, *queries)
	for range *queries {
		if gracefulStopRequested(ctx) {
			err = libraryindex.ErrShutdownRequested
			break
		}
		started := time.Now()
		if _, err = index.Search(ctx, librarysearch.Query{Vector: queryVector, Limit: min(50, *rows), Workers: *workers}); err != nil {
			break
		}
		latencies = append(latencies, time.Since(started))
	}
	err = errors.Join(err, index.Close())
	if err != nil {
		finishMonitor()
		return 1, err
	}
	result.QueryP50, result.QueryP95 = durationPercentile(latencies, .50), durationPercentile(latencies, .95)
	if gracefulStopRequested(ctx) {
		finishMonitor()
		return 130, libraryindex.ErrShutdownRequested
	}

	packPath := filepath.Join(scratch, "synthetic.paipack")
	space := librarypack.VectorSpace{
		Name: "library_mert", Dimension: *dimension, DType: "float32", ByteOrder: "little", Normalized: true,
		Model: "synthetic-benchmark", ModelRevision: "v1", GraphSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Decoder: "synthetic", Preprocessing: "synthetic-four-sparse-v1", Sampling: "synthetic", Pooling: "none", Scope: "synthetic", Missingness: "absent-row",
	}
	exportStart := time.Now()
	_, err = librarypack.WriteSource(ctx, packPath, librarypack.Pack{
		CreatedAt: time.Unix(0, 0).UTC(), CorpusGeneration: "synthetic-scale-v1", MetadataGeneration: "synthetic-metadata-v1",
		MERTGeneration: "synthetic-mert-v1", MERT: space,
	}, &syntheticPackSource{rows: *rows, dimension: *dimension}, librarypack.DefaultLimits())
	result.PackExport = time.Since(exportStart)
	if err == nil && gracefulStopRequested(ctx) {
		err = libraryindex.ErrShutdownRequested
	}
	if err == nil {
		var info os.FileInfo
		info, err = os.Stat(packPath)
		if err == nil {
			result.PackBytes = info.Size()
			result.PackBytesPerTrack = float64(info.Size()) / float64(*rows)
		}
	}
	finishMonitor()
	if err != nil {
		return 1, err
	}
	return 0, json.NewEncoder(stdout).Encode(result)
}

type syntheticScaleSource struct {
	rows, dimension int
	next            int
}

func (s *syntheticScaleSource) Next(ctx context.Context) (librarysearch.VectorRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarysearch.VectorRow{}, false, err
	}
	if s.next >= s.rows {
		return librarysearch.VectorRow{}, false, nil
	}
	row := librarysearch.VectorRow{ID: syntheticID(s.next), Vector: syntheticVector(s.next, s.dimension)}
	s.next++
	return row, true, nil
}

type syntheticPackSource struct{ rows, dimension, next int }

func (s *syntheticPackSource) Next(ctx context.Context) (librarypack.Track, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarypack.Track{}, false, err
	}
	if s.next >= s.rows {
		return librarypack.Track{}, false, nil
	}
	index := s.next
	s.next++
	return librarypack.Track{
		ID: syntheticID(index), Artist: fmt.Sprintf("Synthetic Artist %06d", index%100_000), Title: fmt.Sprintf("Synthetic Track %012d", index),
		SourceIdentity: "synthetic:" + syntheticID(index), MERT: syntheticVector(index, s.dimension),
	}, true, nil
}

func syntheticID(index int) string { return fmt.Sprintf("track-%012d", index) }

func syntheticVector(index, dimension int) []float32 {
	vector := make([]float32, dimension)
	base := index % (dimension / 4)
	step := dimension / 4
	for quarter := range 4 {
		vector[base+quarter*step] = .5
	}
	return vector
}

func monitorProcessRSS(done <-chan struct{}, result chan<- int64) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var peak int64
	for {
		if current := processRSSBytes(); current > peak {
			peak = current
		}
		select {
		case <-done:
			result <- peak
			return
		case <-ticker.C:
		}
	}
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
