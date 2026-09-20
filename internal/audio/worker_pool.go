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
		pool.workers = append(pool.workers, &Worker{Executable: primary.Executable, BundleDir: primary.BundleDir, Model: primary.Model, Device: primary.Device})
	}
	for _, worker := range pool.workers {
		pool.available <- worker
	}
	return pool
}

func (p *WorkerPool) Identity() core.AudioModelIdentity { return p.workers[0].Identity() }
func (p *WorkerPool) Parallelism() int                  { return len(p.workers) }

func (p *WorkerPool) Warm(ctx context.Context) error {
	for _, worker := range p.workers {
		if err := worker.Health(ctx); err != nil {
			return err
		}
	}
	return nil
}

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
	mu          sync.Mutex
	quarantined map[*MERTWorker]bool
	workers     []*MERTWorker
	available   chan *MERTWorker
}

func NewMERTWorkerPool(primary *MERTWorker, parallelism int) *MERTWorkerPool {
	parallelism = max(1, parallelism)
	pool := &MERTWorkerPool{quarantined: make(map[*MERTWorker]bool), workers: make([]*MERTWorker, 0, parallelism), available: make(chan *MERTWorker, parallelism)}
	pool.workers = append(pool.workers, primary)
	for range parallelism - 1 {
		pool.workers = append(pool.workers, &MERTWorker{Executable: primary.Executable, BundleDir: primary.BundleDir, Model: primary.Model, Device: primary.Device, InferenceThreads: primary.InferenceThreads, ResponseTimeout: primary.ResponseTimeout})
	}
	for _, worker := range pool.workers {
		pool.available <- worker
	}
	return pool
}

func (p *MERTWorkerPool) Identity() core.AudioRepresentationIdentity { return p.workers[0].Identity() }
func (p *MERTWorkerPool) Parallelism() int                           { return len(p.workers) }
func (p *MERTWorkerPool) Device() string                             { return p.workers[0].EffectiveDevice() }
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
			errs <- p.Validate(ctx, worker)
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

// Quarantine keeps a failed session unavailable for inference until that exact
// session passes its fixture check. Call while holding its pool reservation.
func (p *MERTWorkerPool) Quarantine(worker *MERTWorker) {
	p.mu.Lock()
	p.quarantined[worker] = true
	p.mu.Unlock()
}

func (p *MERTWorkerPool) Quarantined(worker *MERTWorker) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quarantined[worker]
}

// Validate must be called with the session reserved (or during initial Warm).
func (p *MERTWorkerPool) Validate(ctx context.Context, worker *MERTWorker) error {
	err := worker.Health(ctx)
	p.mu.Lock()
	p.quarantined[worker] = err != nil
	p.mu.Unlock()
	return err
}

// ValidateAll holds each checked reservation until every session is checked,
// so recovery cannot repeatedly validate one healthy session and overlook a
// failed sibling. In-flight calls drain before this completes.
func (p *MERTWorkerPool) ValidateAll(ctx context.Context) error {
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	var errs []error
	for range p.workers {
		worker, release, err := p.Acquire(ctx)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		releases = append(releases, release)
		errs = append(errs, p.Validate(ctx, worker))
	}
	return errors.Join(errs...)
}

func (p *MERTWorkerPool) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	worker, release, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if p.Quarantined(worker) {
		if err := p.Validate(ctx, worker); err != nil {
			return nil, err
		}
	}
	vector, err := worker.EmbedAudio(ctx, pcm)
	if errors.Is(err, ErrNativeWorker) {
		p.Quarantine(worker)
	}
	return vector, err
}

// Acquire reserves an already-warm native session. Callers can wait for a
// session before reserving CPU capacity, preventing queued inference from
// starving decoders and preprocessing.
func (p *MERTWorkerPool) Acquire(ctx context.Context) (*MERTWorker, func(), error) {
	select {
	case worker := <-p.available:
		var once sync.Once
		return worker, func() { once.Do(func() { p.available <- worker }) }, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

// TryAcquire reserves an idle session without waiting. Liveness probes use it
// so health checks never delay queued inference.
func (p *MERTWorkerPool) TryAcquire() (*MERTWorker, func(), bool) {
	select {
	case worker := <-p.available:
		var once sync.Once
		return worker, func() { once.Do(func() { p.available <- worker }) }, true
	default:
		return nil, nil, false
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

func (p *MERTWorkerPool) Diagnostics() MERTWorkerDiagnostics {
	var total MERTWorkerDiagnostics
	for _, worker := range p.workers {
		value := worker.Diagnostics()
		total.NativeFailures += value.NativeFailures
		total.Restarts += value.Restarts
		total.HealthChecks += value.HealthChecks
		total.HealthDuration += value.HealthDuration
		total.RestartDuration += value.RestartDuration
	}
	return total
}
