package libraryindex

import (
	"context"
	"errors"
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
