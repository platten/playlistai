//go:build linux

package process

import (
	"os/exec"
	"syscall"
)

// Owned isolates a native helper in its own process group and asks Linux to
// terminate it if the direct parent dies before the normal close/kill/reap
// sequence can run.
func Owned(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
