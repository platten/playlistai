//go:build linux

package libraryindex

import (
	"io/fs"
	"syscall"
)

func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Dev, stat.Ino
	}
	return 0, 0
}
