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

// ReservationLease permits an operation to relinquish resources when their
// ownership ends (for example source descriptors immediately after decode)
// while retaining the byte reservation for immutable PCM consumed downstream.
type ReservationLease struct {
	admission *Admission
	mu        sync.Mutex
	held      Reservation
	released  bool
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
	lease, err := a.AcquireLease(ctx, request)
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

func (a *Admission) AcquireLease(ctx context.Context, request Reservation) (*ReservationLease, error) {
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
			return &ReservationLease{admission: a, held: request}, nil
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

func (l *ReservationLease) ReleasePart(part Reservation) error {
	if l == nil || l.admission == nil {
		return errors.New("library indexer: invalid reservation lease")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || !reservationContains(l.held, part) {
		return errors.New("library indexer: reservation release exceeds ownership")
	}
	l.admission.release(part)
	l.held.CPU -= part.CPU
	l.held.SourceIO -= part.SourceIO
	l.held.Memory -= part.Memory
	l.held.Files -= part.Files
	l.held.PCMBytes -= part.PCMBytes
	return nil
}

func (l *ReservationLease) Release() {
	if l == nil || l.admission == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.admission.release(l.held)
	l.held = Reservation{}
	l.released = true
}

func reservationContains(held, part Reservation) bool {
	return part.CPU >= 0 && part.SourceIO >= 0 && part.Memory >= 0 && part.Files >= 0 && part.PCMBytes >= 0 &&
		part.CPU <= held.CPU && part.SourceIO <= held.SourceIO && part.Memory <= held.Memory && part.Files <= held.Files && part.PCMBytes <= held.PCMBytes
}

func (a *Admission) release(request Reservation) {
	a.mu.Lock()
	a.used.CPU -= request.CPU
	a.used.SourceIO -= request.SourceIO
	a.used.Memory -= request.Memory
	a.used.Files -= request.Files
	a.used.PCMBytes -= request.PCMBytes
	a.signalLocked()
	a.mu.Unlock()
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
