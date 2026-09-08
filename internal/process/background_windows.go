package process

import (
	"os/exec"
	"syscall"
)

// Background prevents internal helpers from opening a console window on Windows.
// Call before Start or Run; standard streams and cancellation remain unchanged.
func Background(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	// HideWindow alone can still allow a console to flash during process startup.
	const createNoWindow = 0x08000000
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
