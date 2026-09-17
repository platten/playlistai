//go:build !windows

package libraryindex

import (
	"io/fs"
	"syscall"
)

func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		// Stat_t field widths vary across Unix targets (for example, Dev is
		// int32 on Darwin and uint64 on Linux). Normalize the persisted identity
		// explicitly so every supported build compiles with the same schema.
		return uint64(stat.Dev), uint64(stat.Ino)
	}
	return 0, 0
}
