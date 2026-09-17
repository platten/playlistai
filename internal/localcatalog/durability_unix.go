//go:build !windows

package localcatalog

import (
	"errors"
	"os"
)

func syncIndexDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
