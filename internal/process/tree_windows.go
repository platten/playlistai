//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Windows cannot deliver os.Interrupt to a detached process. Closing the
// direct worker is still the graceful cancellation boundary available here;
// native helpers inherit pipe closure and parent-death cleanup.
func InterruptOwned(_ *exec.Cmd) error { return nil }
func KillOwned(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	tree := exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F") //nolint:gosec // fixed system tool and numeric owned PID.
	Background(tree)
	if err := tree.Run(); err == nil {
		return nil
	}
	return killDirect(cmd)
}

func killDirect(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
