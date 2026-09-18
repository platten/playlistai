//go:build windows

package audio

import (
	"os"
	"os/exec"
	"path/filepath"
)

// MERTCUDAHostAvailable is a fast bundle-selection preflight; native provider
// initialization remains authoritative.
func MERTCUDAHostAvailable() bool {
	if root := os.Getenv("SystemRoot"); root != "" {
		if _, err := os.Stat(filepath.Join(root, "System32", "nvcuda.dll")); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("nvidia-smi.exe")
	return err == nil
}
