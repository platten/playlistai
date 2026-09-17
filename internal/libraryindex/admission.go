package libraryindex

import (
	"context"
	"errors"
	"sync"
)

type Reservation struct {
	CPU      int
	SourceIO int
	Memory   int64
	Files    int
	PCMBytes int64
}

type AdmissionUsage struct {
	CPU, SourceIO, Files int
	Memory, PCMBytes     int64
}

// Admission atomically reserves every scarce resource for an operation. No
// caller can hold CPU while waiting for memory (or vice versa), avoiding the
// circular waits common to independently acquired semaphores.
type Admission struct {
	mu       sync.Mutex
	capacity AdmissionUsage
	used     AdmissionUsage
	notify   chan struct{}
	closed   bool
}

func NewAdmission(plan ResourcePlan, pcmByteBudget int64) *Admission {
	memoryCapacity := plan.MaxRAM - plan.ResidentMERTBytes
	if memoryCapacity < 0 {
		memoryCapacity = 0
	}
	if pcmByteBudget <= 0 || pcmByteBudget > memoryCapacity {
		pcmByteBudget = max(int64(16<<20), memoryCapacity/4)
		pcmByteBudget = min(pcmByteBudget, memoryCapacity)
	}
	return &Admission{capacity: AdmissionUsage{CPU: plan.HeavyWorkers, SourceIO: plan.IOWorkers, Files: plan.MaxOpenFiles, Memory: memoryCapacity, PCMBytes: pcmByteBudget}, notify: make(chan struct{})}
}

func (a *Admission) Acquire(ctx context.Context, request Reservation) (func(), error) {
	if request.CPU < 0 || request.SourceIO < 0 || request.Memory < 0 || request.Files < 0 || request.PCMBytes < 0 {
		return nil, errors.New("library indexer: negative resource reservation")
	}
	a.mu.Lock()
	if !a.fitsCapacity(request) {
		a.mu.Unlock()
		return nil, errors.New("library indexer: operation exceeds configured resource capacity")
	}
	for {
		if a.closed {
			a.mu.Unlock()
			return nil, errors.New("library indexer: admission controller is closed")
		}
		if a.available(request) {
			a.used.CPU += request.CPU
			a.used.SourceIO += request.SourceIO
			a.used.Memory += request.Memory
			a.used.Files += request.Files
			a.used.PCMBytes += request.PCMBytes
			a.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					a.mu.Lock()
					a.used.CPU -= request.CPU
					a.used.SourceIO -= request.SourceIO
					a.used.Memory -= request.Memory
					a.used.Files -= request.Files
					a.used.PCMBytes -= request.PCMBytes
					a.signalLocked()
					a.mu.Unlock()
				})
			}, nil
		}
		notify := a.notify
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notify:
		}
		a.mu.Lock()
	}
}

func (a *Admission) fitsCapacity(r Reservation) bool {
	return r.CPU <= a.capacity.CPU && r.SourceIO <= a.capacity.SourceIO && r.Memory <= a.capacity.Memory && r.Files <= a.capacity.Files && r.PCMBytes <= a.capacity.PCMBytes
}

func (a *Admission) available(r Reservation) bool {
	return a.used.CPU+r.CPU <= a.capacity.CPU && a.used.SourceIO+r.SourceIO <= a.capacity.SourceIO &&
		a.used.Memory+r.Memory <= a.capacity.Memory && a.used.Files+r.Files <= a.capacity.Files && a.used.PCMBytes+r.PCMBytes <= a.capacity.PCMBytes
}

func (a *Admission) signalLocked() {
	close(a.notify)
	a.notify = make(chan struct{})
}

func (a *Admission) Usage() (used, capacity AdmissionUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used, a.capacity
}

func (a *Admission) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		a.signalLocked()
	}
}
