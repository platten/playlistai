package updater

import (
	"errors"
	"os"
	"path/filepath"
)

// ClearPreviousExecutables removes backups only from completed update jobs for
// this executable. Pending jobs retain their rollback copy.
func ClearPreviousExecutables() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	roots := []string{filepath.Dir(exe)}
	if cache, err := installerStagingRoot(); err == nil {
		roots = append(roots, cache)
	}
	return clearPreviousExecutables(exe, roots)
}

func clearPreviousExecutables(exe string, roots []string) error {
	var failures []error
	for _, root := range roots {
		dirs, _ := filepath.Glob(filepath.Join(root, ".playlist-ai-update-*"))
		for _, dir := range dirs {
			j, err := readJob(filepath.Join(dir, "job.json"))
			if err != nil || j.Target != exe {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "result.json")); err != nil {
				continue
			}
			if err := os.Remove(filepath.Join(dir, "previous.exe")); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
