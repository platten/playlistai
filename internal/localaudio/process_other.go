//go:build !linux

package localaudio

import "os/exec"

func isolateProcess(_ *exec.Cmd) {}
