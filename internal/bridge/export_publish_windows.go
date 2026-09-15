//go:build windows

package bridge

import "golang.org/x/sys/windows"

// MoveFileEx without REPLACE_EXISTING atomically publishes on NTFS and FAT/
// exFAT without requiring hard-link support or overwriting a racing file.
func publishCSVNoReplace(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, 0)
}
