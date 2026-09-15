package installlock

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockSubprocess(t *testing.T) {
	path := os.Getenv("PLAYLISTAI_TEST_INSTALL_LOCK")
	if path == "" {
		return
	}
	release, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	fmt.Println("locked")
	// The parent kills this child to exercise kernel-owned lock cleanup.
	time.Sleep(time.Minute)
}

func TestLockSurvivesMarkersAndReleasesOnProcessDeath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.lock")
	if err := os.WriteFile(path, []byte("legacy marker"), 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestLockSubprocess$")
	child.Env = append(os.Environ(), "PLAYLISTAI_TEST_INSTALL_LOCK="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatalf("child not ready: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not acquire lock")
	}
	if release, err := TryAcquire(path); !errors.Is(err, ErrBusy) {
		if release != nil {
			_ = release()
		}
		t.Fatalf("live owner not rejected: %v", err)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	release, err := TryAcquire(path)
	if err != nil {
		t.Fatalf("dead owner blocked retry: %v", err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("lock inode removed: %v", err)
	}
}

func TestSharedReadersExcludeInstallerUntilBothRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	openReader := func() func() error {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		release, err := TryAcquireSharedFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = release() })
		return release
	}
	first, second := openReader(), openReader()
	assertWriterBlocked := func() {
		t.Helper()
		release, err := TryAcquire(path)
		if release != nil {
			_ = release()
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("reader failed to exclude installer: %v", err)
		}
	}
	assertWriterBlocked()
	if err := first(); err != nil {
		t.Fatal(err)
	}
	if err := first(); err != nil {
		t.Fatalf("reader release not idempotent: %v", err)
	}
	assertWriterBlocked()
	if err := second(); err != nil {
		t.Fatal(err)
	}
	writer, err := TryAcquire(path)
	if err != nil {
		t.Fatalf("released readers block installer: %v", err)
	}
	defer func() { _ = writer() }()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := TryAcquireSharedFile(f); !errors.Is(err, ErrBusy) {
		if release != nil {
			_ = release()
		}
		t.Fatalf("installer failed to exclude reader: %v", err)
	}
	if f.Fd() != ^uintptr(0) {
		t.Fatal("failed reader acquisition did not close its file")
	}
}
