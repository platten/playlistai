//go:build !windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// InterruptOwned asks an owned process group to stop. Owned must have been
// called before Start; signaling the group also reaches native descendants.
func InterruptOwned(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// KillOwned forcibly terminates an owned process group.
func KillOwned(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
