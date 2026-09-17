//go:build windows

package librarypack

import "golang.org/x/sys/windows"

func atomicReplaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx WRITE_THROUGH flushes the move. Windows does not expose the same
// portable directory-fsync operation used by Unix filesystems.
func syncDirectory(string) error { return nil }
