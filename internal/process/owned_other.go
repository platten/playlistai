//go:build !linux && !windows

package process

import (
	"os/exec"
	"syscall"
)

// Owned isolates the helper in a process group. Platforms without Linux's
// parent-death signal still receive explicit group interruption and reaping.
func Owned(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
