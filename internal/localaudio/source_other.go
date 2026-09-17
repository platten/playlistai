//go:build !linux

package localaudio

import (
	"fmt"
	"os"
	"path/filepath"
)

// Non-Linux implementations exist so the desktop repository continues to
// compile. The packaged codec runtime itself is Linux-only and its manifest
// validation rejects use on these platforms.
func sourceRevision(path string) (SourceRevision, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return SourceRevision{}, fmt.Errorf("localaudio: source path must be absolute and clean")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return SourceRevision{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return SourceRevision{}, fmt.Errorf("localaudio: source must be a regular non-symlink file")
	}
	return SourceRevision{Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano()}, nil
}
