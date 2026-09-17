package localaudio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "emit":
		fmt.Print(strings.Repeat("x", 1024))
	case "sleep":
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func TestRunBoundedRejectsOutputOverflow(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runBounded(context.Background(), 5*time.Second, executable,
		[]string{"-test.run=^TestProcessHelper$", "--", "emit"}, 32, 32)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("output limit error = %v", err)
	}
}

func TestRunBoundedKillsCanceledProcessGroup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err = runBounded(ctx, 5*time.Second, executable,
		[]string{"-test.run=^TestProcessHelper$", "--", "sleep"}, 32, 32)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("canceled process result = %v after %v", err, time.Since(started))
	}
}
