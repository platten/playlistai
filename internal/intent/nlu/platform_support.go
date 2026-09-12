package nlu

import (
	"fmt"
	"strconv"
	"strings"
)

const macOSRuntimeRequirement = "compact intent models require macOS 14 or newer"

// checkMacOSVersion checks the deployment target of the checksum-pinned ONNX
// runtime. The desktop itself retains its older minimum supported OS version.
func checkMacOSVersion(version string) error {
	version = strings.TrimSpace(version)
	parts := strings.Split(version, ".")
	invalid := func() error { return fmt.Errorf("nlu: cannot determine macOS version; %s", macOSRuntimeRequirement) }
	if len(parts) < 1 || len(parts) > 3 {
		return invalid()
	}
	major := 0
	for i, part := range parts {
		if part == "" {
			return invalid()
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return invalid()
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return invalid()
		}
		if i == 0 {
			major = value
		}
	}
	if major < 14 {
		return fmt.Errorf("nlu: %s; this Mac runs macOS %s", macOSRuntimeRequirement, version)
	}
	return nil
}
