//go:build !windows

package process

import "os/exec"

// Background leaves process startup unchanged on non-Windows platforms.
func Background(_ *exec.Cmd) {}
