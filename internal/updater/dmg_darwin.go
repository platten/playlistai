package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func unpackDMG(parent context.Context, archive, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	mount := filepath.Join(dir, "mounted")
	if err := os.Mkdir(mount, 0700); err != nil {
		return "", err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mount, archive).Run(); err != nil {
		return "", fmt.Errorf("cannot open the verified update disk image: %w", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "/usr/bin/hdiutil", "detach", "-force", mount).Run()
	}()
	app := filepath.Join(mount, "playlist-ai.app")
	if _, err := treeHash(app); err != nil {
		return "", err
	}
	payload := filepath.Join(dir, "playlist-ai.app")
	if err := exec.CommandContext(ctx, "/usr/bin/ditto", app, payload).Run(); err != nil {
		return "", err
	}
	return payload, nil
}
