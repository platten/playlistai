//go:build windows

package process

import "os/exec"

// Owned gives a native helper the same non-console window behavior as other
// managed background processes on Windows. Explicit handle cleanup remains the
// lifecycle fence on this platform.
func Owned(cmd *exec.Cmd) { Background(cmd) }
