// Package installlock serializes installation and activation across processes.
package installlock

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// ErrBusy means another process still owns the installation lock.
var ErrBusy = errors.New("installation target is in use")

// TryAcquire takes a nonblocking OS lock. The file is deliberately retained:
// deleting it would allow two processes to lock different inodes at one path.
// Existing files, including markers left by an interrupted legacy install, do
// not imply ownership. The operating system releases ownership on process death.
func TryAcquire(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return TryAcquireFile(f)
}

// TryAcquireFile takes ownership of an already opened file, allowing callers to
// constrain lookup through os.Root. It closes the file on failure or release.
func TryAcquireFile(f *os.File) (func() error, error) {
	return tryAcquireFile(f, false)
}

// TryAcquireSharedFile retains a read lease alongside other readers while
// excluding an installer. It closes the supplied file on failure or release;
// callers may open the lock file read-only. Release is safe to call repeatedly.
func TryAcquireSharedFile(f *os.File) (func() error, error) {
	return tryAcquireFile(f, true)
}

func tryAcquireFile(f *os.File, shared bool) (func() error, error) {
	if f == nil {
		return nil, errors.New("installation lock file is nil")
	}
	if err := lock(f, shared); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire installation lock: %w", err)
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() { releaseErr = errors.Join(unlock(f), f.Close()) })
		return releaseErr
	}, nil
}
