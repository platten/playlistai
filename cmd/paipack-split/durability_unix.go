//go:build !windows

package main

import (
	"errors"
	"os"
)

func syncParentDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
