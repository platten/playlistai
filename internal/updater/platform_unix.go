//go:build !windows

package updater

import (
	"errors"
	"syscall"
)

func checkOtherInstances(string) error { return nil }

func processAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return err == nil, err
}
