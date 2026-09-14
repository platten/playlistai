package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Check the complete image path so unrelated portable installations do not
// block this update. Never terminate another instance or its in-flight work.
func checkOtherInstances(target string) error {
	return checkInstancesOutsideProcess(target, uint32(os.Getpid()))
}

func checkInstancesOutsideProcess(target string, ownerPID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("check running Playlist AI copies: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		// The app's directly owned audio workers use this executable too. They
		// are retired during normal shutdown before the installer is launched.
		if entry.ProcessID == ownerPID || entry.ParentProcessID == ownerPID || !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(target)) {
			continue
		}
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			continue // Protected or exited process; the installer also checks writes.
		}
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		queryErr := windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		_ = windows.CloseHandle(h)
		if queryErr == nil && strings.EqualFold(windows.UTF16ToString(buf[:size]), target) {
			return fmt.Errorf("another copy of Playlist AI is running; close the other Playlist AI windows and wait for background analysis to finish, then retry the update")
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("check running Playlist AI copies: %w", err)
	}
	return nil
}

func processAlive(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	status, err := windows.WaitForSingleObject(h, 0)
	return status == uint32(windows.WAIT_TIMEOUT), err
}
