//go:build !linux && !darwin && !windows

package discoveryasset

func checkDisk(string, int64) error { return nil }
