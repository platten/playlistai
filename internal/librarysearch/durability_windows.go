//go:build windows

package librarysearch

// Individual generation files are flushed before publication. Windows has no
// portable directory-fsync operation; rename publication supplies the barrier.
func syncDirectory(string) error { return nil }
