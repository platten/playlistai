package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     windows.Handle
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   windows.Handle
	IDList     uintptr
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       windows.Handle
	Process    windows.Handle
}

func runInstaller(installer, targetDir, expectedHash string) error {
	name, err := windows.UTF16PtrFromString(installer)
	if err != nil {
		return err
	}
	// Keep the verified file locked against writes/deletion through execution.
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if hash, err := treeHash(installer); err != nil || hash != expectedHash {
		return fmt.Errorf("verified installer changed before execution")
	}
	// This helper runs without elevation. Re-establish publisher provenance
	// independently of the writable local job before asking Windows to elevate
	// an executable. Keep its file locked throughout verification and launch.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	offer, asset, err := check(ctx, "0.0.0", installation{Kind: "windows-installer", Arch: runtime.GOARCH}, releaseClient(15*time.Second), latestURL)
	if err != nil {
		return err
	}
	if !offer.CanInstall {
		return fmt.Errorf("GitHub no longer supplies a verified installer for this computer")
	}
	f, err := os.Open(installer)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, io.LimitReader(f, asset.Size+1))
	_ = f.Close()
	if readErr != nil || size != asset.Size || hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(asset.Digest, "sha256:") {
		return fmt.Errorf("the installer no longer matches GitHub's latest release; retry the update")
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	// NSIS requires /D to be last and unquoted, including paths with spaces.
	// ShellExecuteEx receives the executable separately; no command shell is used.
	parameters, err := windows.UTF16PtrFromString("/S /D=" + targetDir)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		return err
	}
	defer windows.CoUninitialize()
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: name, Parameters: parameters, Show: 1}
	info.Size = uint32(unsafe.Sizeof(info))
	r, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return fmt.Errorf("installer approval in Windows failed or was canceled: %w", callErr)
	}
	if info.Process == 0 {
		return fmt.Errorf("installer process unavailable from Windows")
	}
	defer func() { _ = windows.CloseHandle(info.Process) }()
	if _, err := windows.WaitForSingleObject(info.Process, windows.INFINITE); err != nil {
		return err
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(info.Process, &exitCode); err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("installer exited with code %d", exitCode)
	}
	return nil
}
