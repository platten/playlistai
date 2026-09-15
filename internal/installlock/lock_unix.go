//go:build !windows

package installlock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lock(f *os.File, shared bool) error {
	flags := unix.LOCK_EX | unix.LOCK_NB
	if shared {
		flags = unix.LOCK_SH | unix.LOCK_NB
	}
	err := unix.Flock(int(f.Fd()), flags)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrBusy
	}
	return err
}

func unlock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
