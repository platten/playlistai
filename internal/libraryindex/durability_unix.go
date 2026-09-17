//go:build !windows

package libraryindex

import (
	"errors"
	"os"
)

func syncFileAndDirectory(path, directory string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	fileErr := errors.Join(file.Sync(), file.Close())
	dir, err := os.Open(directory)
	if err != nil {
		return errors.Join(fileErr, err)
	}
	return errors.Join(fileErr, dir.Sync(), dir.Close())
}
