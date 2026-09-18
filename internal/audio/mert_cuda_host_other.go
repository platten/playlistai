//go:build !linux && !windows

package audio

// MERTCUDAHostAvailable reports false where CUDA MERT is unsupported.
func MERTCUDAHostAvailable() bool { return false }
