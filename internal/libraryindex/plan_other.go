//go:build !linux

package libraryindex

// detectHostCapacity keeps non-Linux desktop builds conservative. The turnkey
// analyzer is currently supported on Linux, where plan_linux.go also accounts
// for affinity, cgroup quotas, available memory, and descriptor limits. Other
// platforms use the Go runtime's effective scheduler width and let the shared
// planner apply its documented 2 GiB/1024-descriptor fallbacks.
func detectHostCapacity() hostCapacity {
	slots := runtimeCPUSlots()
	return hostCapacity{
		LogicalCPUs:   slots,
		PhysicalCores: slots,
		CPUSlots:      slots,
		ComputeSlots:  slots,
		CPUSource:     "gomaxprocs-logical-fallback",
		GOMAXPROCS:    slots,
	}
}
