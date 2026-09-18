package libraryindex

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAdmissionIsAtomicAndCancellationUnblocks(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 2, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 16}
	a := NewAdmission(plan, 1024)
	defer a.Close()
	release, err := a.Acquire(context.Background(), Reservation{CPU: 1, SourceIO: 1, Memory: 128, Files: 2, PCMBytes: 900})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := a.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 128, Files: 2, PCMBytes: 200}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked reservation did not cancel: %v", err)
	}
	used, _ := a.Usage()
	if used.CPU != 1 || used.SourceIO != 1 || used.PCMBytes != 900 {
		t.Fatalf("partial resources leaked while waiting: %+v", used)
	}
	release()
	release()
	used, _ = a.Usage()
	if used != (AdmissionUsage{}) {
		t.Fatalf("idempotent release leaked resources: %+v", used)
	}
}

func TestAdmissionRejectsImpossibleReservation(t *testing.T) {
	a := NewAdmission(ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}, 1024)
	defer a.Close()
	if _, err := a.Acquire(context.Background(), Reservation{CPU: 2}); err == nil {
		t.Fatal("impossible CPU request waited forever instead of failing")
	}
}

func TestAdmissionSubtractsResidentNativeMemory(t *testing.T) {
	plan := ResourcePlan{
		HeavyWorkers: 1, IOWorkers: 1, MaxOpenFiles: 8,
		MaxRAM: 2 << 30, ResidentMERTBytes: 768 << 20,
	}
	a := NewAdmission(plan, 256<<20)
	defer a.Close()
	_, capacity := a.Usage()
	if want := int64(1280 << 20); capacity.Memory != want {
		t.Fatalf("memory capacity=%d want %d", capacity.Memory, want)
	}
	if release, err := a.Acquire(context.Background(), Reservation{Memory: capacity.Memory + 1}); err == nil {
		release()
		t.Fatal("reservation beyond post-residency memory unexpectedly admitted")
	}
}

func TestAdmissionUsesPlannedPCMBufferByDefault(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxOpenFiles: 8, MaxRAM: 8 << 30, BufferedPCMBytes: 1 << 30}
	a := NewAdmission(plan, 0)
	defer a.Close()
	_, capacity := a.Usage()
	if capacity.PCMBytes != 1<<30 {
		t.Fatalf("PCM capacity=%d want %d", capacity.PCMBytes, int64(1<<30))
	}
}

func TestAdmissionGrantsEqualRequestsFIFO(t *testing.T) {
	a := NewAdmission(ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}, 1024)
	defer a.Close()
	hold, err := a.Acquire(context.Background(), Reservation{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	type grant struct {
		id      int
		release func()
		err     error
	}
	grants := make(chan grant, 2)
	for id := 1; id <= 2; id++ {
		id := id
		go func() {
			release, err := a.Acquire(context.Background(), Reservation{CPU: 1})
			grants <- grant{id: id, release: release, err: err}
		}()
		for deadline := time.Now().Add(time.Second); ; {
			if a.Stats().Queued >= uint64(id) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("waiter did not queue")
			}
			time.Sleep(time.Millisecond)
		}
	}
	hold()
	first := <-grants
	if first.err != nil || first.id != 1 {
		t.Fatalf("first grant=%+v", first)
	}
	select {
	case second := <-grants:
		t.Fatalf("second waiter granted before first release: %+v", second)
	default:
	}
	first.release()
	second := <-grants
	if second.err != nil || second.id != 2 {
		t.Fatalf("second grant=%+v", second)
	}
	second.release()
}

func TestAdmissionOldestFittingBypassesBlockedTrackBuffer(t *testing.T) {
	a := NewAdmission(ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}, 1000)
	defer a.Close()
	pcmHold, err := a.Acquire(context.Background(), Reservation{PCMBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	large := make(chan func(), 1)
	go func() {
		release, _ := a.Acquire(context.Background(), Reservation{PCMBytes: 900})
		large <- release
	}()
	for a.Stats().Queued < 1 {
		time.Sleep(time.Millisecond)
	}
	cpu, err := a.Acquire(context.Background(), Reservation{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	cpu()
	small := make(chan func(), 1)
	go func() {
		release, _ := a.Acquire(context.Background(), Reservation{PCMBytes: 200})
		small <- release
	}()
	for a.Stats().Queued < 3 {
		time.Sleep(time.Millisecond)
	}
	select {
	case <-large:
		t.Fatal("PCM waiter granted beyond capacity")
	case <-small:
		t.Fatal("younger PCM waiter bypassed an older request")
	default:
	}
	pcmHold()
	largeRelease := <-large
	select {
	case <-small:
		t.Fatal("younger PCM waiter granted before the older lease released")
	default:
	}
	largeRelease()
	smallRelease := <-small
	smallRelease()
	stats := a.Stats()
	if stats.FIFOBlocked == 0 || stats.PCMBlocked == 0 {
		t.Fatalf("queue blockers not recorded: %+v", stats)
	}
}

func TestAdmissionCanceledWaiterIsRemovedAndCloseWakesWaiters(t *testing.T) {
	a := NewAdmission(ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}, 1024)
	hold, err := a.Acquire(context.Background(), Reservation{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		_, err := a.Acquire(ctx, Reservation{CPU: 1})
		canceled <- err
	}()
	for a.Stats().Queued < 1 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error=%v", err)
	}

	closed := make(chan error, 1)
	go func() {
		_, err := a.Acquire(context.Background(), Reservation{CPU: 1})
		closed <- err
	}()
	for a.Stats().Queued < 2 {
		time.Sleep(time.Millisecond)
	}
	a.Close()
	if err := <-closed; err == nil || err.Error() != "library indexer: admission controller is closed" {
		t.Fatalf("closed waiter error=%v", err)
	}
	hold()
}

func TestAdmissionConcurrentReleasePartLeavesNoUsage(t *testing.T) {
	a := NewAdmission(ResourcePlan{HeavyWorkers: 4, IOWorkers: 2, MaxRAM: 1 << 30, MaxOpenFiles: 16}, 4096)
	defer a.Close()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := a.AcquireLease(context.Background(), Reservation{CPU: 1, PCMBytes: 512})
			if err != nil {
				t.Error(err)
				return
			}
			if err := lease.ReleasePart(Reservation{CPU: 1}); err != nil {
				t.Error(err)
			}
			lease.Release()
		}()
	}
	wg.Wait()
	used, _ := a.Usage()
	if used != (AdmissionUsage{}) {
		t.Fatalf("concurrent leases leaked: %+v", used)
	}
}
