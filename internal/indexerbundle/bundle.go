// Package indexerbundle reads the reproducible payload appended to a
// playlist-indexer launcher. The outer executable is one installable file;
// native codec/model assets are checksum-verified again by their owners after
// extraction.
package indexerbundle

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	trailerSize = 16
	magic       = "PAIBNDL2"
)

var ErrNotBundled = errors.New("playlist-indexer: executable has no embedded payload")

type Bundle struct {
	file *os.File
	zip  *zip.Reader
}

func OpenExecutable(path string) (*Bundle, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() < trailerSize {
		file.Close()
		return nil, ErrNotBundled
	}
	trailer := make([]byte, trailerSize)
	if _, err := file.ReadAt(trailer, info.Size()-trailerSize); err != nil {
		file.Close()
		return nil, err
	}
	if string(trailer[8:]) != magic {
		file.Close()
		return nil, ErrNotBundled
	}
	size := int64(binary.LittleEndian.Uint64(trailer[:8]))
	start := info.Size() - trailerSize - size
	if size <= 0 || start < 0 {
		file.Close()
		return nil, errors.New("playlist-indexer: invalid embedded payload bounds")
	}
	zr, err := zip.NewReader(io.NewSectionReader(file, start, size), size)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("playlist-indexer: open embedded payload: %w", err)
	}
	return &Bundle{file: file, zip: zr}, nil
}

func OpenSelf() (*Bundle, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return OpenExecutable(executable)
}

func (b *Bundle) Close() error {
	if b == nil || b.file == nil {
		return nil
	}
	return b.file.Close()
}

func (b *Bundle) Sub(name string) (fs.FS, error) {
	if b == nil || b.zip == nil {
		return nil, ErrNotBundled
	}
	return fs.Sub(b.zip, name)
}

// Extract copies one payload subtree without following links. The caller must
// use a newly-created private directory and authenticate the inner manifest.
func (b *Bundle) Extract(name, destination string) error {
	sub, err := b.Sub(name)
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if !fs.ValidPath(path) || strings.Contains(path, "\\") {
			return errors.New("playlist-indexer: unsafe embedded payload path")
		}
		target := filepath.Join(destination, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("playlist-indexer: embedded payload contains a non-regular file")
		}
		in, err := sub.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		mode := os.FileMode(0o600)
		if info, infoErr := entry.Info(); infoErr == nil && info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		syncErr := out.Sync()
		closeErr := out.Close()
		return errors.Join(copyErr, syncErr, closeErr)
	})
}

func Trailer(payloadSize uint64) []byte {
	out := make([]byte, trailerSize)
	binary.LittleEndian.PutUint64(out[:8], payloadSize)
	copy(out[8:], magic)
	return out
}
