//go:build windows

package libraryindex

import (
	"errors"
	"os"
)

func syncFileAndDirectory(path, _ string) error {
	// FlushFileBuffers requires a handle opened with write access. Windows has
	// no portable equivalent of syncing the containing directory.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
