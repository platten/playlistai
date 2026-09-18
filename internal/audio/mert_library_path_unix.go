//go:build !windows

package audio

import (
	"runtime"
)

func prependMERTLibraryPath(environment []string, directory string) []string {
	name := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		name = "DYLD_LIBRARY_PATH"
	}
	return prependMERTEnvironmentPath(environment, name, directory, false)
}
