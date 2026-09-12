//go:build windows && cgo

package audioruntime

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/platten/playlistai/internal/audio"
)

// Load the verified runtime with its own directory first for dependencies.
// onnxruntime_go subsequently reuses this module via LoadLibrary. This is local
// to the isolated MERT child and does not change CLAP's loader or global PATH.
func prepareMERTRuntime(dir string, m audio.MERTBundleManifest) (func(), error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	handle, err := windows.LoadLibraryEx(m.File(absolute, "runtime"), 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("MERT app-local runtime load failed: %w", err)
	}
	release := func() { _ = windows.FreeLibrary(handle) }
	for role, name := range audio.MERTWindowsRuntimeDependencies(m.Platform) {
		moduleName, err := windows.UTF16PtrFromString(name)
		if err != nil {
			release()
			return nil, err
		}
		var module windows.Handle
		if err = windows.GetModuleHandleEx(0, moduleName, &module); err != nil {
			release()
			return nil, fmt.Errorf("MERT app-local dependency is not loaded: %s", name)
		}
		path := make([]uint16, 32768)
		n, err := windows.GetModuleFileName(module, &path[0], uint32(len(path)))
		_ = windows.FreeLibrary(module)
		if err != nil || n == 0 || n >= uint32(len(path)) {
			release()
			return nil, fmt.Errorf("cannot verify loaded MERT dependency: %s", name)
		}
		actual := filepath.Clean(windows.UTF16ToString(path[:n]))
		if !strings.EqualFold(actual, filepath.Clean(m.File(absolute, role))) {
			release()
			return nil, fmt.Errorf("MERT dependency did not load from verified pack: %s", name)
		}
	}
	return release, nil
}
