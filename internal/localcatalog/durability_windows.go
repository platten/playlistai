//go:build windows

package localcatalog

// Windows does not support fsync on directory handles. Each index file is
// flushed before atomic publication; directory durability is best effort.
func syncIndexDirectory(string) error { return nil }
