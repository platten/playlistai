package libraryannotate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type SubsetFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type SubsetManifest struct {
	Version   string       `json:"version"`
	Selection string       `json:"selection"`
	Files     []SubsetFile `json:"files"`
	Bytes     int64        `json:"bytes"`
}

// Subset copies a bounded lexical sample to a new directory. Copies keep later
// tooling from changing original tags and work without symlink privileges.
func Subset(ctx context.Context, root, out string, limit int, maxBytes int64) (SubsetManifest, error) {
	return SubsetSelection(ctx, root, out, limit, maxBytes, nil)
}

// SubsetSelection optionally uses an explicit reviewable list of relative paths.
func SubsetSelection(ctx context.Context, root, out string, limit int, maxBytes int64, paths []string) (manifest SubsetManifest, err error) {
	if limit < 1 || limit > 512 || maxBytes < 1 {
		return manifest, errors.New("subset needs 1..512 files and a positive byte budget")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return manifest, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return manifest, err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return manifest, err
	}
	if _, e := os.Lstat(out); !errors.Is(e, os.ErrNotExist) {
		return manifest, errors.New("subset output must not already exist")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(out))
	if err != nil {
		return manifest, err
	}
	out = filepath.Join(parent, filepath.Base(out))
	rel, err := filepath.Rel(root, out)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return manifest, errors.New("subset output must be outside the source tree")
	}
	source, err := os.OpenRoot(root)
	if err != nil {
		return manifest, err
	}
	defer source.Close()
	staging, err := os.MkdirTemp(parent, ".playlist-subset-*")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(staging)
	manifest = SubsetManifest{Version: "library-subset/v1", Selection: "lexical-first; not a representative listening sample", Files: []SubsetFile{}}
	visited := 0
	visit := func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > 100000 {
			return errors.New("subset directory-entry budget exceeded")
		}
		if len(manifest.Files) >= limit {
			return fs.SkipAll
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".flac", ".mp3", ".aac", ".m4a", ".mp4":
		default:
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		in, err := source.Open(relative)
		if err != nil {
			return err
		}
		defer in.Close()
		before, err := in.Stat()
		if err != nil {
			return err
		}
		if !before.Mode().IsRegular() {
			return nil
		}
		if before.Size() > maxBytes-manifest.Bytes {
			return fmt.Errorf("subset byte budget would be exceeded by file %d", len(manifest.Files)+1)
		}
		target := filepath.Join(staging, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(file, h), io.LimitReader(contextReader{ctx, in}, before.Size()+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		after, err := in.Stat()
		if err != nil {
			return err
		}
		if n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return errors.New("source changed during subset copy")
		}
		manifest.Files = append(manifest.Files, SubsetFile{filepath.ToSlash(relative), n, hex.EncodeToString(h.Sum(nil))})
		manifest.Bytes += n
		return nil
	}
	if len(paths) == 0 {
		err = filepath.WalkDir(root, visit)
	} else {
		if len(paths) > limit {
			return manifest, errors.New("explicit file list exceeds subset limit")
		}
		paths = slices.Clone(paths)
		slices.Sort(paths)
		manifest.Selection = "explicit-file-list; not independent listening labels"
		for i, relative := range paths {
			if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || i > 0 && relative == paths[i-1] {
				return manifest, errors.New("file list must contain unique clean relative paths")
			}
			info, statErr := source.Lstat(relative)
			if statErr != nil {
				return manifest, statErr
			}
			if !info.Mode().IsRegular() {
				return manifest, errors.New("file list entries must be regular files")
			}
			count := len(manifest.Files)
			err = visit(filepath.Join(root, relative), fs.FileInfoToDirEntry(info), nil)
			if err != nil {
				break
			}
			if len(manifest.Files) == count {
				return manifest, errors.New("file list entry has an unsupported audio extension")
			}
		}
	}
	if err != nil {
		return manifest, err
	}
	if len(manifest.Files) == 0 {
		return manifest, errors.New("source contains no supported audio files")
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	if err = os.WriteFile(filepath.Join(staging, "subset-manifest.json"), raw, 0600); err != nil {
		return manifest, err
	}
	if err = ctx.Err(); err != nil {
		return manifest, err
	}
	err = os.Rename(staging, out)
	return manifest, err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
