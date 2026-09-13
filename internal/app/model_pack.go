package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/modelpack"
	"github.com/platten/playlistai/internal/ports"
)

// prepareModelPack preserves directory imports and adds verified segmented
// manifests. Only a fresh extraction directory is removed; resumable compressed
// pieces remain in a separate cache. Activation remains the installer's job.
func (c *Container) prepareModelPack(ctx context.Context, source, op string, p ports.Progress) (string, func(), error) {
	source = strings.TrimSpace(source)
	noop := func() {}
	if source == "" {
		return "", noop, fmt.Errorf("model pack source is required")
	}
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		return source, noop, nil
	}
	root := filepath.Join(c.cfg.DataDir, "model-downloads")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", noop, err
	}
	work, err := os.MkdirTemp(root, "unpack-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(work) }
	destination := filepath.Join(work, "files")
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(op+"\n"+source)))
	if err := modelpack.Fetch(ctx, source, filepath.Join(root, key), destination, modelPackProgress{p, op}); err != nil {
		cleanup()
		return "", noop, err
	}
	return destination, cleanup, nil
}

type modelPackProgress struct {
	p  ports.Progress
	op string
}

func (p modelPackProgress) Report(_ string, done, total int64, note string) {
	if p.p != nil {
		p.p.Report(p.op, done, total, note)
	}
}
