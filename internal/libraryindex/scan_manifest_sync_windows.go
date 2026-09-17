//go:build windows

package libraryindex

// Windows rejects Sync on directory handles opened through os.Open. Manifest
// files themselves are flushed before the atomic directory rename; the Linux
// playlist-indexer release additionally fsyncs both directories.
func syncScanManifestDirectory(string) error { return nil }
