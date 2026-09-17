//go:build !windows

package localaudio

import (
	"errors"
	"io/fs"
	"os"
)

func executableModeValid(info fs.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
