package updater

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/platten/playlistai/internal/process"
)

func TestVerifyInstalledVersion(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, actual string
		wantError    bool
	}{
		{"updated", "0.14.2", false},
		{"unchanged after successful installer exit", "0.14.1", true},
		{"wrong package", "0.14.3", true},
		{"invalid version", "dev", true},
		{"empty output", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PLAYLISTAI_TEST_INSTALLED_VERSION", tc.actual)
			err := verifyInstalledVersion(executable, "v0.14.2")
			if (err != nil) != tc.wantError {
				t.Fatalf("verify installed %q: %v", tc.actual, err)
			}
		})
	}
	if err := verifyInstalledVersion(filepath.Join(t.TempDir(), "missing.exe"), "0.14.2"); err == nil {
		t.Fatal("missing application accepted")
	}
}

func TestOtherInstancesUseInstalledPathAndIgnoreSelf(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOtherInstances(executable); err != nil {
		t.Fatalf("current process must not block its own update: %v", err)
	}
	// A separate installation with the same filename must not block us.
	other := filepath.Join(t.TempDir(), filepath.Base(executable))
	if err := copyExecutable(executable, other); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(other, "--test-update-parent")
	process.Background(child)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	if err := checkOtherInstances(executable); err != nil {
		t.Fatalf("unrelated installation blocked update: %v", err)
	}
	unrelated := filepath.Join(t.TempDir(), filepath.Base(executable))
	if err := checkInstancesOutsideProcess(unrelated, ^uint32(0)); err != nil {
		t.Fatalf("same filename at different paths blocked update: %v", err)
	}
	if err := checkOtherInstances(other); err != nil {
		t.Fatalf("owned worker should be allowed to close during shutdown: %v", err)
	}
	// From an unrelated application's perspective, this is another live copy.
	if err := checkInstancesOutsideProcess(other, ^uint32(0)); err == nil || !strings.Contains(err.Error(), "another copy") {
		t.Fatalf("running installed executable must block update: %v", err)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if err := checkInstancesOutsideProcess(other, ^uint32(0)); err != nil {
		t.Fatalf("closed copy still blocks update: %v", err)
	}
}

// Each case owns a locked thread. Letting the goroutine exit while locked
// retires that thread, so even a failing regression cannot leak COM state.
func installerCOMThread(t *testing.T, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		run()
	}()
	<-done
}

func TestInstallerCOMAlreadyInitialized(t *testing.T) {
	installerCOMThread(t, func() {
		if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
			t.Errorf("prepare STA: %v", err)
			return
		}
		called := false
		err := withInstallerCOM(func() error { called = true; return nil })
		if err != nil || !called {
			t.Errorf("already initialized STA must still execute installer: called=%v, err=%v", called, err)
			return
		}
		// Our initialization must remain active after the helper returns.
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); !errors.Is(err, syscall.Errno(0x80010106)) {
			t.Errorf("helper uninitialized caller's apartment: %v", err)
			return
		}
		windows.CoUninitialize()
		// No extra S_FALSE reference may remain after balancing the caller.
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
			t.Errorf("helper leaked COM initialization: %v", err)
			return
		}
		windows.CoUninitialize()
	})
}

func TestInstallerCOMFreshThreadCleansUpAfterExecutionError(t *testing.T) {
	installerCOMThread(t, func() {
		want := errors.New("installer launch failed")
		if err := withInstallerCOM(func() error { return want }); !errors.Is(err, want) {
			t.Errorf("execution error lost: %v", err)
		}
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
			t.Errorf("COM not released after execution error: %v", err)
			return
		}
		windows.CoUninitialize()
	})
}

func TestInstallerCOMRejectsIncompatibleApartment(t *testing.T) {
	installerCOMThread(t, func() {
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
			t.Errorf("prepare MTA: %v", err)
			return
		}
		called := false
		err := withInstallerCOM(func() error { called = true; return nil })
		if !errors.Is(err, syscall.Errno(0x80010106)) || called {
			t.Errorf("incompatible apartment must prevent execution: called=%v, err=%v", called, err)
		}
		// A failed initialization must not consume the caller's COM reference.
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); !errors.Is(err, syscall.Errno(1)) {
			t.Errorf("failed initialization changed caller's apartment: %v", err)
			return
		}
		windows.CoUninitialize()
		windows.CoUninitialize()
	})
}
