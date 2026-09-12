//go:build windows && cgo

package nlu

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func prepareRuntime(path string) (func(), error) {
	// Resolve dependent DLLs beside the verified runtime. This changes neither
	// the global PATH nor DLL lookup in the desktop or other inference workers.
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("nlu: app-local runtime load failed: %w", err)
	}
	return func() { _ = windows.FreeLibrary(handle) }, nil
}
