package nlu

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/ports"
)

// ImportAssets accepts only the exact pinned base encoders and tokenizers.
// A distribution manifest cannot replace them with a trained task model or
// supply an arbitrary native library. The regular installer supplies the
// platform runtime and embedded notices, then the application checks health.
func ImportAssets(ctx context.Context, root, source string, p ports.Progress) error {
	if err := CheckPackagedRuntime(); err != nil {
		return err
	}
	if err := importSources(ctx, root, source, Sources()); err != nil {
		return err
	}
	return InstallAssets(ctx, root, p)
}

func importSources(ctx context.Context, root, source string, sources []Source) error {
	// Validate the complete source before changing installed assets.
	for _, s := range sources {
		if s.Setup {
			if err := checkImportSource(ctx, sourcePath(source, s), s); err != nil {
				return err
			}
		}
	}
	for _, s := range sources {
		if !s.Setup {
			continue
		}
		target := sourcePath(AssetDir(root), s)
		if err := checkImportSource(ctx, target, s); err == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := copyImportSource(ctx, sourcePath(source, s), target, s); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func checkImportSource(ctx context.Context, path string, s Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != s.Size {
		return fmt.Errorf("intent pack: invalid file size for %s", s.Name)
	}
	h := sha256.New()
	_, err = io.Copy(h, importReader{ctx, f})
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != s.SHA256 {
		return fmt.Errorf("intent pack: checksum mismatch for %s", s.Name)
	}
	return nil
}

func copyImportSource(ctx context.Context, source, target string, s Source) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(target), ".model-import-*")
	if err != nil {
		return err
	}
	name := out.Name()
	defer func() { _ = out.Close(); _ = os.Remove(name) }()
	if _, err = io.Copy(out, io.LimitReader(importReader{ctx, in}, s.Size+1)); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = checkImportSource(ctx, name, s); err != nil {
		return err
	}
	return os.Rename(name, target)
}

type importReader struct {
	ctx context.Context
	r   io.Reader
}

func (r importReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
