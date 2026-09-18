//go:build linux

package audio

import (
	"os"
	"os/exec"
)

// MERTCUDAHostAvailable is a fast preflight used only to choose between two
// embedded offline bundles. Native provider initialization remains the
// authoritative capability and parity check.
func MERTCUDAHostAvailable() bool {
	for _, path := range []string{"/dev/nvidiactl", "/dev/nvidia0", "/dev/dxg"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("nvidia-smi")
	return err == nil
}
