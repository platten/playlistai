//go:build !linux

package main

import "runtime"

func processRSSBytes() int64 {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return int64(memory.Sys) // portable fallback; reported as runtime memory, not OS RSS
}
