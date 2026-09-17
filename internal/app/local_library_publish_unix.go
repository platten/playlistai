//go:build !windows

package app

import "os"

func publishLocalLibrarySettings(source, target string) error { return os.Rename(source, target) }

func syncLocalLibraryDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
