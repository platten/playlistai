//go:build !linux

package audio

func processResidentBytes(int) int64 { return 0 }
