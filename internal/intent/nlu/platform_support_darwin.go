//go:build darwin

package nlu

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// PlatformSupportError reports OS-version requirements for the pinned native
// text runtime. Compiled inference and architecture checks remain in setup.
// This check reads kernel metadata without launching a shell or loading a model.
func PlatformSupportError() error {
	version, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return fmt.Errorf("nlu: cannot determine macOS version; %s", macOSRuntimeRequirement)
	}
	return checkMacOSVersion(version)
}
