//go:build !windows

package process

import (
	"os/exec"
	"testing"
)

func TestBackgroundPreservesCommand(t *testing.T) {
	cmd := exec.Command("fixture", "argument")
	cmd.Dir = "fixture-dir"
	Background(cmd)
	if cmd.Path != "fixture" || cmd.Dir != "fixture-dir" || len(cmd.Args) != 2 || cmd.SysProcAttr != nil {
		t.Fatal("background changed non-Windows command")
	}
}
