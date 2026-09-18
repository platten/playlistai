//go:build !linux

package main

func readBenchmarkHostDiagnostics(_ string, device string) benchmarkHostDiagnostics {
	return benchmarkHostDiagnostics{Filesystem: "unknown", GPUDevice: device}
}

func readBenchmarkARCSnapshot() benchmarkARCSnapshot { return benchmarkARCSnapshot{} }
