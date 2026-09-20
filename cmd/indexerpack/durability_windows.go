//go:build windows

package main

func syncParentDirectory(string) error {
	// Windows has no portable equivalent of syncing a containing directory.
	// The completed output file was flushed before the atomic rename.
	return nil
}
