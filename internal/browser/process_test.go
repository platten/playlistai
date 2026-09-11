package browser

import (
	"os"
	"os/exec"
	"testing"
)

func TestDefaultProcessStarterLaunchesAndReapsChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Exercise process dispatch without opening a user's browser.
	cmd := exec.Command(executable, "-test.run=^$")
	if err := startProc(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
