//go:build windows

package main

func syncParentDirectory(string) error {
	// Windows has no portable equivalent of syncing a containing directory.
	return nil
}
