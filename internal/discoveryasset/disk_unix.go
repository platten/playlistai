//go:build linux || darwin

package discoveryasset

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func checkDisk(path string, required int64) error {
	var s unix.Statfs_t
	if e := unix.Statfs(path, &s); e != nil {
		return e
	}
	available := s.Bavail * uint64(s.Bsize)
	if available < uint64(required) {
		return fmt.Errorf("discoveryasset: insufficient disk space: need %d bytes, available %d", required, available)
	}
	return nil
}
