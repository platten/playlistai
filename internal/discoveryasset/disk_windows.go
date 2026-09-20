//go:build windows

package discoveryasset

import (
	"fmt"
	"golang.org/x/sys/windows"
)

func checkDisk(path string, required int64) error {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	var available uint64
	if e = windows.GetDiskFreeSpaceEx(p, &available, nil, nil); e != nil {
		return e
	}
	if available < uint64(required) {
		return fmt.Errorf("discoveryasset: insufficient disk space: need %d bytes, available %d", required, available)
	}
	return nil
}
