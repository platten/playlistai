package libraryindex

import (
	"context"
	"errors"
	"sync"
	"time"
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

type AdmissionWaitCounters struct {
	PeakMemoryBytes int64         `json:"peakMemoryBytes"`
	PeakPCMBytes    int64         `json:"peakPCMBytes"`
	Requests        uint64        `json:"requests"`
	Immediate       uint64        `json:"immediate"`
	Queued          uint64        `json:"queued"`
	Granted         uint64        `json:"granted"`
	Canceled        uint64        `json:"canceled"`
	FIFOBlocked     uint64        `json:"fifoBlocked"`
	CPUBlocked      uint64        `json:"cpuBlocked"`
	SourceIOBlocked uint64        `json:"sourceIoBlocked"`
	MemoryBlocked   uint64        `json:"memoryBlocked"`
	FilesBlocked    uint64        `json:"filesBlocked"`
	PCMBlocked      uint64        `json:"pcmBlocked"`
	WaitDuration    time.Duration `json:"waitDuration"`
}

type admissionWaiter struct {
	request Reservation
	ready   chan struct{}
	queued  time.Time
	granted bool
	err     error
}

// Admission atomically reserves every scarce resource for an operation. No
// caller can hold CPU while waiting for memory (or vice versa), avoiding the
// circular waits common to independently acquired semaphores.
type Admission struct {
	mu       sync.Mutex
	capacity AdmissionUsage
	used     AdmissionUsage
	waiters  []*admissionWaiter
	stats    AdmissionWaitCounters
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
	if pcmByteBudget <= 0 {
		pcmByteBudget = plan.BufferedPCMBytes
	}
	if pcmByteBudget <= 0 || pcmByteBudget > memoryCapacity {
		pcmByteBudget = max(int64(16<<20), memoryCapacity/4)
		pcmByteBudget = min(pcmByteBudget, memoryCapacity)
	}
	return &Admission{capacity: AdmissionUsage{CPU: plan.HeavyWorkers, SourceIO: plan.IOWorkers, Files: plan.MaxOpenFiles, Memory: memoryCapacity, PCMBytes: pcmByteBudget}}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.stats.Requests++
	if !a.fitsCapacity(request) {
		a.mu.Unlock()
		return nil, errors.New("library indexer: operation exceeds configured resource capacity")
	}
	if a.closed {
		a.mu.Unlock()
		return nil, errors.New("library indexer: admission controller is closed")
	}
	if len(a.waiters) == 0 && a.available(request) {
		a.reserveLocked(request)
		a.stats.Immediate++
		a.stats.Granted++
		a.mu.Unlock()
		return &ReservationLease{admission: a, held: request}, nil
	}
	waiter := &admissionWaiter{request: request, ready: make(chan struct{}), queued: time.Now()}
	a.recordBlockedLocked(request, len(a.waiters) > 0)
	a.waiters = append(a.waiters, waiter)
	a.stats.Queued++
	a.grantLocked()
	a.mu.Unlock()

	select {
	case <-waiter.ready:
		if waiter.err != nil {
			return nil, waiter.err
		}
		return &ReservationLease{admission: a, held: request}, nil
	case <-ctx.Done():
		a.mu.Lock()
		if waiter.err != nil {
			err := waiter.err
			a.mu.Unlock()
			return nil, err
		}
		if waiter.granted {
			a.releaseLocked(request)
		} else {
			a.removeWaiterLocked(waiter)
			a.stats.WaitDuration += time.Since(waiter.queued)
		}
		a.stats.Canceled++
		a.grantLocked()
		a.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (a *Admission) reserveLocked(request Reservation) {
	a.used.CPU += request.CPU
	a.used.SourceIO += request.SourceIO
	a.used.Memory += request.Memory
	a.used.Files += request.Files
	a.used.PCMBytes += request.PCMBytes
	a.stats.PeakMemoryBytes = max(a.stats.PeakMemoryBytes, a.used.Memory)
	a.stats.PeakPCMBytes = max(a.stats.PeakPCMBytes, a.used.PCMBytes)
}

func (a *Admission) releaseLocked(request Reservation) {
	a.used.CPU -= request.CPU
	a.used.SourceIO -= request.SourceIO
	a.used.Memory -= request.Memory
	a.used.Files -= request.Files
	a.used.PCMBytes -= request.PCMBytes
}

func (a *Admission) recordBlockedLocked(request Reservation, fifoBlocked bool) {
	if fifoBlocked {
		a.stats.FIFOBlocked++
	}
	if a.used.CPU+request.CPU > a.capacity.CPU {
		a.stats.CPUBlocked++
	}
	if a.used.SourceIO+request.SourceIO > a.capacity.SourceIO {
		a.stats.SourceIOBlocked++
	}
	if a.used.Memory+request.Memory > a.capacity.Memory {
		a.stats.MemoryBlocked++
	}
	if a.used.Files+request.Files > a.capacity.Files {
		a.stats.FilesBlocked++
	}
	if a.used.PCMBytes+request.PCMBytes > a.capacity.PCMBytes {
		a.stats.PCMBlocked++
	}
}

func (a *Admission) grantLocked() {
	if a.closed {
		return
	}
	for {
		granted := -1
		var protected Reservation
		for i, waiter := range a.waiters {
			if a.available(waiter.request) && !reservationConflicts(waiter.request, protected) {
				granted = i
				break
			}
			protected = mergeReservationMask(protected, a.blockedMask(waiter.request))
		}
		if granted < 0 {
			return
		}
		waiter := a.waiters[granted]
		copy(a.waiters[granted:], a.waiters[granted+1:])
		a.waiters[len(a.waiters)-1] = nil
		a.waiters = a.waiters[:len(a.waiters)-1]
		a.reserveLocked(waiter.request)
		waiter.granted = true
		a.stats.Granted++
		a.stats.WaitDuration += time.Since(waiter.queued)
		close(waiter.ready)
	}
}

func (a *Admission) blockedMask(request Reservation) Reservation {
	var blocked Reservation
	if a.used.CPU+request.CPU > a.capacity.CPU {
		blocked.CPU = 1
	}
	if a.used.SourceIO+request.SourceIO > a.capacity.SourceIO {
		blocked.SourceIO = 1
	}
	if a.used.Memory+request.Memory > a.capacity.Memory {
		blocked.Memory = 1
	}
	if a.used.Files+request.Files > a.capacity.Files {
		blocked.Files = 1
	}
	if a.used.PCMBytes+request.PCMBytes > a.capacity.PCMBytes {
		blocked.PCMBytes = 1
	}
	return blocked
}

func reservationConflicts(request, protected Reservation) bool {
	return protected.CPU != 0 && request.CPU != 0 || protected.SourceIO != 0 && request.SourceIO != 0 ||
		protected.Memory != 0 && request.Memory != 0 || protected.Files != 0 && request.Files != 0 ||
		protected.PCMBytes != 0 && request.PCMBytes != 0
}

func mergeReservationMask(left, right Reservation) Reservation {
	if right.CPU != 0 {
		left.CPU = 1
	}
	if right.SourceIO != 0 {
		left.SourceIO = 1
	}
	if right.Memory != 0 {
		left.Memory = 1
	}
	if right.Files != 0 {
		left.Files = 1
	}
	if right.PCMBytes != 0 {
		left.PCMBytes = 1
	}
	return left
}

func (a *Admission) removeWaiterLocked(target *admissionWaiter) {
	for i, waiter := range a.waiters {
		if waiter != target {
			continue
		}
		copy(a.waiters[i:], a.waiters[i+1:])
		a.waiters[len(a.waiters)-1] = nil
		a.waiters = a.waiters[:len(a.waiters)-1]
		return
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
	a.releaseLocked(request)
	a.grantLocked()
	a.mu.Unlock()
}

func (a *Admission) fitsCapacity(r Reservation) bool {
	return r.CPU <= a.capacity.CPU && r.SourceIO <= a.capacity.SourceIO && r.Memory <= a.capacity.Memory && r.Files <= a.capacity.Files && r.PCMBytes <= a.capacity.PCMBytes
}

func (a *Admission) available(r Reservation) bool {
	return a.used.CPU+r.CPU <= a.capacity.CPU && a.used.SourceIO+r.SourceIO <= a.capacity.SourceIO &&
		a.used.Memory+r.Memory <= a.capacity.Memory && a.used.Files+r.Files <= a.capacity.Files && a.used.PCMBytes+r.PCMBytes <= a.capacity.PCMBytes
}

func (a *Admission) Usage() (used, capacity AdmissionUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used, a.capacity
}

func (a *Admission) Stats() AdmissionWaitCounters {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}

func (a *Admission) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		err := errors.New("library indexer: admission controller is closed")
		for _, waiter := range a.waiters {
			waiter.err = err
			a.stats.WaitDuration += time.Since(waiter.queued)
			close(waiter.ready)
		}
		clear(a.waiters)
		a.waiters = nil
	}
}
