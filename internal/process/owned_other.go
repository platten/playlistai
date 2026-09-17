//go:build !linux && !windows

package process

import "os/exec"

// Owned applies the platform's managed background-process policy. Platforms
// without a parent-death primitive still rely on pipes and explicit reap.
func Owned(cmd *exec.Cmd) { Background(cmd) }
