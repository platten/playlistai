//go:build windows

package libraryindex

import "io/fs"

// The indexer is currently released only for Linux. Keeping the package
// buildable for desktop cross-platform tests preserves conservative identity:
// path-derived IDs are used when native volume/file IDs are unavailable.
func fileIdentity(fs.FileInfo) (uint64, uint64) { return 0, 0 }
