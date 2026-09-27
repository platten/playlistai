package audio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// artifactLayoutValid checks the files that the worker will verify before it
// loads native code. It deliberately does not trust their contents or hash them.
func artifactLayoutValid(ctx context.Context, dir string, a BundleArtifact) error {
	for _, file := range []struct {
		name string
		size int64
	}{{a.Name, a.Size}, {filepath.Base(a.ArchiveMember), a.UnpackedSize}} {
		if file.size == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(dir, file.name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != file.size {
			return fmt.Errorf("audio: bundle artifact layout invalid: %s", file.name)
		}
	}
	return nil
}
