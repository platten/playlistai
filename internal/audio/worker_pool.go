package audio

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/platten/playlistai/internal/core"
)

const maxAnalysisParallelism = 4

// AnalysisParallelism uses one two-thread ONNX worker per two available Go
// processors. The cap bounds duplicated model memory on high-core machines.
func AnalysisParallelism() int { return analysisParallelism(runtime.GOMAXPROCS(0)) }

func analysisParallelism(processors int) int {
	return min(maxAnalysisParallelism, max(1, processors/2))
}

// WorkerPool lazily starts independent CLAP worker processes as concurrent
// requests arrive. A single request retains the same analyzer identity.
type WorkerPool struct {
	workers   []*Worker
	available chan *Worker
}

func NewWorkerPool(primary *Worker, parallelism int) *WorkerPool {
	parallelism = max(1, parallelism)
	pool := &WorkerPool{workers: make([]*Worker, 0, parallelism), available: make(chan *Worker, parallelism)}
	pool.workers = append(pool.workers, primary)
	for range parallelism - 1 {
		pool.workers = append(pool.workers, &Worker{Executable: primary.Executable, BundleDir: primary.BundleDir, Model: primary.Model})
	}
	for _, worker := range pool.workers {
		pool.available <- worker
	}
	return pool
}

func (p *WorkerPool) Identity() core.AudioModelIdentity { return p.workers[0].Identity() }
func (p *WorkerPool) Parallelism() int                  { return len(p.workers) }

func (p *WorkerPool) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	return p.call(ctx, func(worker *Worker) ([]float32, error) { return worker.EmbedAudio(ctx, pcm) })
}

func (p *WorkerPool) EmbedText(ctx context.Context, prompt string) ([]float32, error) {
	return p.call(ctx, func(worker *Worker) ([]float32, error) { return worker.EmbedText(ctx, prompt) })
}

func (p *WorkerPool) call(ctx context.Context, fn func(*Worker) ([]float32, error)) ([]float32, error) {
	select {
	case worker := <-p.available:
		defer func() { p.available <- worker }()
		return fn(worker)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *WorkerPool) Unload() {
	for _, worker := range p.workers {
		worker.Unload()
	}
}

func (p *WorkerPool) Close() error {
	var errs []error
	for _, worker := range p.workers {
		errs = append(errs, worker.Close())
	}
	return errors.Join(errs...)
}

// MERTWorkerPool provides the same bounded concurrency for optional audio-only
// representations. Workers remain lazy, so unused slots consume no model RAM.
type MERTWorkerPool struct {
	workers   []*MERTWorker
	available chan *MERTWorker
}

func NewMERTWorkerPool(primary *MERTWorker, parallelism int) *MERTWorkerPool {
	parallelism = max(1, parallelism)
	pool := &MERTWorkerPool{workers: make([]*MERTWorker, 0, parallelism), available: make(chan *MERTWorker, parallelism)}
	pool.workers = append(pool.workers, primary)
	for range parallelism - 1 {
		pool.workers = append(pool.workers, &MERTWorker{Executable: primary.Executable, BundleDir: primary.BundleDir, Model: primary.Model, InferenceThreads: primary.InferenceThreads})
	}
	for _, worker := range pool.workers {
		pool.available <- worker
	}
	return pool
}

func (p *MERTWorkerPool) Identity() core.AudioRepresentationIdentity { return p.workers[0].Identity() }
func (p *MERTWorkerPool) Parallelism() int                           { return len(p.workers) }
func (p *MERTWorkerPool) ResidentBytes() int64 {
	var total int64
	for _, worker := range p.workers {
		total += worker.ResidentBytes()
	}
	return total
}

// Warm starts and validates every independent native session. Automatic
// planning can measure this operation before admitting duplicate residency.
func (p *MERTWorkerPool) Warm(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(p.workers))
	for _, worker := range p.workers {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- worker.Health(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	var joined []error
	for err := range errs {
		joined = append(joined, err)
	}
	return errors.Join(joined...)
}

func (p *MERTWorkerPool) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	select {
	case worker := <-p.available:
		defer func() { p.available <- worker }()
		return worker.EmbedAudio(ctx, pcm)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *MERTWorkerPool) Unload() {
	for _, worker := range p.workers {
		worker.Unload()
	}
}

func (p *MERTWorkerPool) Close() error {
	var errs []error
	for _, worker := range p.workers {
		errs = append(errs, worker.Close())
	}
	return errors.Join(errs...)
}
