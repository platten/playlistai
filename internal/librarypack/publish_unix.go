//go:build !windows

package librarypack

import "os"

func atomicReplaceFile(source, target string) error { return os.Rename(source, target) }

func syncDirectory(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
