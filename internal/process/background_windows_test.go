package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackgroundHasNoConsoleAndPreservesOutput(t *testing.T) {
	if os.Getenv("PLAYLISTAI_CONSOLE_TEST_CHILD") == "1" {
		window, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
		fmt.Printf("console=%d\n", window)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBackgroundHasNoConsoleAndPreservesOutput$")
	cmd.Env = append(os.Environ(), "PLAYLISTAI_CONSOLE_TEST_CHILD=1")
	Background(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "console=0\n") {
		t.Fatalf("helper acquired a console or lost stdout: %s", out)
	}
}
