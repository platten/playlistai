package modelpack

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const DefaultPartBytes int64 = 190_000_000

type sourceFile struct {
	manifest File
	path     string
}

// Package creates a deterministic, checksummed multipart tar.zst distribution.
// The source tree is never modified and output must be a new or empty directory.
func Package(ctx context.Context, name, source, output string, partBytes int64) (Manifest, error) {
	var manifest Manifest
	if !safeName(name) {
		return manifest, errors.New("model pack name must contain only letters, digits, dot, underscore, or hyphen")
	}
	if partBytes == 0 {
		partBytes = DefaultPartBytes
	}
	if partBytes < 1024 || partBytes >= MaxPartBytes {
		return manifest, fmt.Errorf("model pack part size must be between 1024 and %d bytes (exclusive)", MaxPartBytes)
	}
	files, err := collectSourceFiles(ctx, source)
	if err != nil {
		return manifest, err
	}
	if len(files) == 0 {
		return manifest, errors.New("model pack source is empty")
	}
	if err = ensureEmptyDirectory(output); err != nil {
		return manifest, err
	}

	w := &packageSplitWriter{ctx: ctx, dir: output, limit: partBytes}
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(2), zstd.WithWindowSize(8<<20))
	if err != nil {
		return manifest, err
	}
	tw := tar.NewWriter(zw)
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			break
		}
		header := &tar.Header{Name: file.manifest.Path, Mode: 0o644, Size: file.manifest.Size, ModTime: unixEpoch, Format: tar.FormatUSTAR}
		if err = tw.WriteHeader(header); err != nil {
			break
		}
		var input *os.File
		input, err = os.Open(file.path)
		if err == nil {
			_, err = io.Copy(tw, contextReader{ctx: ctx, r: input})
		}
		err = errors.Join(err, closeFile(input))
		if err != nil {
			break
		}
	}
	err = errors.Join(err, tw.Close(), zw.Close(), w.Close())
	if err != nil {
		return manifest, err
	}
	for _, file := range files {
		actual, hashErr := hashRegularFile(ctx, file.path)
		actual.Path = file.manifest.Path
		if hashErr != nil || actual != file.manifest {
			return manifest, fmt.Errorf("model pack source changed while packaging: %s", file.manifest.Path)
		}
	}
	manifest = Manifest{Version: 1, Name: name, Parts: w.parts, Files: make([]File, len(files))}
	for i := range files {
		manifest.Files[i] = files[i].manifest
	}
	if err = manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err = os.WriteFile(filepath.Join(output, "manifest.json"), append(raw, '\n'), 0o600); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

var unixEpoch = time.Unix(0, 0).UTC()

func safeName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func ensureEmptyDirectory(dir string) error {
	if err := os.Mkdir(dir, 0o700); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("model pack output directory is not empty")
	}
	return nil
}

func collectSourceFiles(ctx context.Context, root string) ([]sourceFile, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("model pack source must be a directory")
	}
	var files []sourceFile
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("model pack source contains a non-regular file: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !safePath(relative) {
			return fmt.Errorf("model pack source contains an unsafe path: %s", relative)
		}
		file, err := hashRegularFile(ctx, path)
		if err != nil {
			return err
		}
		file.Path = relative
		files = append(files, sourceFile{manifest: file, path: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].manifest.Path < files[j].manifest.Path })
	return files, nil
}

func hashRegularFile(ctx context.Context, path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return File{}, errors.New("model pack source file is invalid or too large")
	}
	h := sha256.New()
	n, err := io.Copy(h, contextReader{ctx: ctx, r: f})
	return File{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

func closeFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

type packageSplitWriter struct {
	ctx   context.Context
	dir   string
	limit int64
	file  *os.File
	hash  hash.Hash
	size  int64
	parts []Part
}

func (w *packageSplitWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		if w.file == nil {
			name := fmt.Sprintf("archive.tar.zst.part%03d", len(w.parts)+1)
			file, err := os.OpenFile(filepath.Join(w.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return written, err
			}
			w.file, w.hash, w.size = file, sha256.New(), 0
		}
		n := int(min(int64(len(data)), w.limit-w.size))
		count, err := w.file.Write(data[:n])
		if count > 0 {
			_, _ = w.hash.Write(data[:count])
			w.size += int64(count)
			written += count
			data = data[count:]
		}
		if err != nil {
			return written, err
		}
		if w.size == w.limit {
			if err := w.closePart(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *packageSplitWriter) closePart() error {
	if w.file == nil {
		return nil
	}
	name := filepath.Base(w.file.Name())
	err := errors.Join(w.file.Sync(), w.file.Close())
	if err == nil {
		w.parts = append(w.parts, Part{Path: name, Size: w.size, SHA256: hex.EncodeToString(w.hash.Sum(nil))})
	}
	w.file, w.hash, w.size = nil, nil, 0
	return err
}

func (w *packageSplitWriter) Close() error { return w.closePart() }
