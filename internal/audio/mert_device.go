package audio

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolveMERTDevice binds a CLI preference to the execution provider carried
// by a verified bundle. A CPU-only runtime can never masquerade as CUDA.
func ResolveMERTDevice(requested, backend string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "auto"
	}
	if backend != "cpu" && backend != "cuda" {
		return "", fmt.Errorf("audio: unsupported MERT bundle backend %q", backend)
	}
	if requested == "auto" {
		if backend == "cuda" {
			return "cuda:0", nil
		}
		return "cpu", nil
	}
	if requested == "cpu" {
		if backend != "cpu" {
			return "", fmt.Errorf("audio: --device cpu requires a CPU MERT bundle")
		}
		return "cpu", nil
	}
	if requested == "cuda" {
		requested = "cuda:0"
	}
	prefix, rawIndex, found := strings.Cut(requested, ":")
	index, err := strconv.Atoi(rawIndex)
	if !found || prefix != "cuda" || err != nil || index < 0 || index > 63 {
		return "", fmt.Errorf("audio: --device must be auto, cpu, cuda, or cuda:INDEX")
	}
	if backend != "cuda" {
		return "", fmt.Errorf("audio: %s requires a parity-validated CUDA MERT bundle", requested)
	}
	return fmt.Sprintf("cuda:%d", index), nil
}

func MERTCUDADeviceIndex(device string) (int, bool) {
	prefix, rawIndex, found := strings.Cut(device, ":")
	index, err := strconv.Atoi(rawIndex)
	return index, found && prefix == "cuda" && err == nil && index >= 0 && index <= 63
}
