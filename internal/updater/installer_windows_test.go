package updater

import (
	"errors"
	"runtime"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

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
