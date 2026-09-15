//go:build !windows

package bridge

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func publishCSVNoReplace(source, target string) error {
	err := os.Link(source, target)
	if err == nil || !(errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EOPNOTSUPP)) {
		return err
	}
	// Some removable filesystems lack hard links. Exclusive creation still
	// prevents overwriting other data; this fallback is not atomically visible.
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	owned, err := output.Stat()
	if err != nil {
		_ = output.Close()
		return err
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			if current, err := os.Lstat(target); err == nil && os.SameFile(owned, current) {
				_ = os.Remove(target)
			}
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
