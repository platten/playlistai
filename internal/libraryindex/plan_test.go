package libraryindex

import "testing"

func testCapacity() hostCapacity {
	return hostCapacity{CPUSlots: 8, CPUQuota: 8, CPUSource: "test", AvailableRAM: 16 << 30, OpenFileSoft: 1024, GOMAXPROCS: 8}
}

func TestResourcePlanAutoAndManual(t *testing.T) {
	auto, err := resolveResourcePlan(ResourceOverrides{}, testCapacity())
	if err != nil {
		t.Fatal(err)
	}
	if auto.Mode != ConcurrencyAuto || auto.HeavyWorkers != 7 || auto.InferenceWorkers != 2 || auto.InferenceWorkers*auto.InferenceThreads > auto.HeavyWorkers || auto.IOWorkers != 2 || auto.DecodeWorkers != 4 {
		t.Fatalf("unexpected auto plan: %+v", auto)
	}
	manual, err := resolveResourcePlan(ResourceOverrides{Mode: ConcurrencyManual, Workers: 8, IOProfile: IONAS, IOWorkers: 2, DecodeWorkers: 2, DSPWorkers: 2, InferenceWorkers: 2, InferenceThreads: 2, MaxRAM: 8 << 30}, testCapacity())
	if err != nil {
		t.Fatal(err)
	}
	if manual.HeavyWorkers != 8 || manual.DecodeWorkers != 2 || manual.InferenceThreads != 2 || manual.MaxRAM != 8<<30 {
		t.Fatalf("unexpected manual plan: %+v", manual)
	}
}

func TestResourcePlanSerialRejectsConflicts(t *testing.T) {
	if _, err := resolveResourcePlan(ResourceOverrides{Mode: ConcurrencySerial, DecodeWorkers: 2}, testCapacity()); err == nil {
		t.Fatal("serial accepted conflicting decode workers")
	}
	serial, err := resolveResourcePlan(ResourceOverrides{Mode: ConcurrencySerial}, testCapacity())
	if err != nil {
		t.Fatal(err)
	}
	if serial.HeavyWorkers != 1 || serial.IOWorkers != 1 || serial.InferenceWorkers != 1 || serial.InferenceThreads != 1 {
		t.Fatalf("serial plan is not a one-heavy-unit baseline: %+v", serial)
	}
}

func TestResourcePlanRejectsImpossibleInferenceAndDescriptorReservations(t *testing.T) {
	if _, err := resolveResourcePlan(ResourceOverrides{Mode: ConcurrencyManual, Workers: 4, InferenceWorkers: 2, InferenceThreads: 3}, testCapacity()); err == nil {
		t.Fatal("accepted an inference reservation over the CPU budget")
	}
	if _, err := resolveResourcePlan(ResourceOverrides{MaxOpenFiles: 1000}, testCapacity()); err == nil {
		t.Fatal("accepted an open-file budget without headroom")
	}
	if _, err := resolveResourcePlan(ResourceOverrides{MaxRAM: 128 << 20}, testCapacity()); err == nil {
		t.Fatal("accepted too little RAM for one operation")
	}
	if _, err := resolveResourcePlan(ResourceOverrides{MaxRAM: 17 << 30}, testCapacity()); err == nil {
		t.Fatal("accepted a RAM target above current host/cgroup headroom")
	}
}

func TestResourcePlanAcceptsExplicitRAMWhenAvailabilityIsUnknown(t *testing.T) {
	host := testCapacity()
	host.AvailableRAM = 0
	plan, err := resolveResourcePlan(ResourceOverrides{Mode: ConcurrencyManual, Workers: 2, MaxRAM: 4 << 30}, host)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MaxRAM != 4<<30 {
		t.Fatalf("explicit RAM target was not preserved: %+v", plan)
	}
}

func TestMetadataPlanDoesNotReserveMERT(t *testing.T) {
	plan, err := resolveResourcePlanFor(ResourceOverrides{Mode: ConcurrencyAuto, MaxRAM: minimumOperationBytes}, testCapacity(), false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.InferenceWorkers != 0 || plan.InferenceThreads != 0 {
		t.Fatalf("metadata plan reserved MERT: %+v", plan)
	}
}
