//go:build linux

package localaudio

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

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
	revision := SourceRevision{Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		revision.Device = stat.Dev
		revision.Inode = stat.Ino
		revision.ChangeUnixNano = stat.Ctim.Sec*1e9 + stat.Ctim.Nsec
	}
	return revision, nil
}
