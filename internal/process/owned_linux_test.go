//go:build linux

package process

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestOwnedUsesProcessGroupAndParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	Owned(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("unexpected owned process attributes: %+v", cmd.SysProcAttr)
	}
}
