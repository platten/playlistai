package librarypack

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// WriteIndexed packages the verified prebuilt search and discovery indexes of
// a staged v7 generation as a v8 paipack. The semantic PackID remains bound to
// the source payload; the archive hash also binds the derived index payload.
func WriteIndexed(ctx context.Context, out string, generation *Generation, limits Limits) (Manifest, error) {
	limits = limits.normalized()
	if generation == nil || generation.manifest.Version != FormatVersion || generation.Directory() == "" {
		return Manifest{}, errors.New("librarypack: indexed export requires a verified v7 generation")
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Manifest{}, err
	}
	work, err := os.MkdirTemp(filepath.Dir(abs), ".paipack-indexed-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(work)
	root := generation.Directory()
	paths := []string{"discovery.sqlite"}
	err = filepath.WalkDir(filepath.Join(root, "local-index-v3"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("librarypack: nonregular index file %q", path)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	sort.Strings(paths)
	manifest := generation.Manifest()
	manifest.Version = IndexedFormatVersion
	manifest.IndexFiles = make([]IndexedFile, 0, len(paths))
	for _, path := range paths {
		if !validIndexedPath(path) {
			return Manifest{}, fmt.Errorf("librarypack: unsafe index path %q", path)
		}
		info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if statErr != nil || !info.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("librarypack: invalid index source %q", path)
		}
		entry, hashErr := hashFile(ctx, filepath.Join(root, filepath.FromSlash(path)))
		if hashErr != nil {
			return Manifest{}, hashErr
		}
		manifest.IndexFiles = append(manifest.IndexFiles, IndexedFile{Path: path, Size: entry.Size, SHA256: entry.SHA256})
	}
	bundlePath := filepath.Join(work, IndexBundleName)
	if err := writeIndexBundle(ctx, bundlePath, root, manifest.IndexFiles); err != nil {
		return Manifest{}, err
	}
	bundle, err := hashFile(ctx, bundlePath)
	if err != nil {
		return Manifest{}, err
	}
	bundle.Name, bundle.Kind = IndexBundleName, "prebuilt_indexes_tar"
	manifest.Files = append(manifest.Files, bundle)
	manifest.PackID = semanticID(manifest)
	if manifest.PackID != generation.manifest.PackID {
		return Manifest{}, errors.New("librarypack: indexed export changed semantic pack identity")
	}
	if err := manifest.Validate(limits); err != nil {
		return Manifest{}, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > limits.MaxManifestBytes {
		return Manifest{}, errors.New("librarypack: indexed manifest exceeds limit")
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".paipack-output-*")
	if err != nil {
		return Manifest{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	clapPath := ""
	if manifest.Coverage.CLAP > 0 {
		clapPath = filepath.Join(root, CLAPVectorsName)
	}
	err = writeArchive(ctx, tmp, raw, filepath.Join(root, MetadataName), filepath.Join(root, MERTVectorsName), clapPath, bundlePath)
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(tmpName)
	if err != nil {
		return Manifest{}, err
	}
	if info.Size() > limits.MaxArchiveBytes {
		return Manifest{}, errors.New("librarypack: indexed archive exceeds limit")
	}
	if err := atomicReplaceFile(tmpName, abs); err != nil {
		return Manifest{}, err
	}
	if err := syncDirectory(filepath.Dir(abs)); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func writeIndexBundle(ctx context.Context, path, root string, files []IndexedFile) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(out)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			_ = tw.Close()
			_ = out.Close()
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: f.Path, Typeflag: tar.TypeReg, Mode: 0o600, Size: f.Size, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}); err != nil {
			_ = tw.Close()
			_ = out.Close()
			return err
		}
		in, err := os.Open(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			_ = tw.Close()
			_ = out.Close()
			return err
		}
		h := sha256.New()
		written, copyErr := io.CopyN(tw, io.TeeReader(&contextReader{ctx: ctx, reader: in}, h), f.Size)
		closeErr := in.Close()
		if err := errors.Join(copyErr, closeErr); err != nil || written != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			_ = tw.Close()
			_ = out.Close()
			return fmt.Errorf("librarypack: index changed during export: %s", f.Path)
		}
	}
	if err := tw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	return errors.Join(out.Sync(), out.Close())
}

func extractIndexBundle(ctx context.Context, destination string, manifest Manifest, limits Limits) error {
	bundlePath := filepath.Join(destination, IndexBundleName)
	in, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer in.Close()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	want := make(map[string]IndexedFile, len(manifest.IndexFiles))
	for _, f := range manifest.IndexFiles {
		want[f.Path] = f
	}
	seen := make(map[string]bool, len(want))
	reader := tar.NewReader(&contextReader{ctx: ctx, reader: in})
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("librarypack: embedded index tar: %w", nextErr)
		}
		f, ok := want[header.Name]
		if !ok || seen[header.Name] || header.Typeflag != tar.TypeReg || !validIndexedPath(header.Name) || header.Size != f.Size || header.Size > limits.MaxMemberBytes {
			return fmt.Errorf("librarypack: invalid embedded index member %q", header.Name)
		}
		seen[header.Name] = true
		if err := os.MkdirAll(filepath.Join(destination, filepath.Dir(filepath.FromSlash(f.Path))), 0o700); err != nil {
			return err
		}
		out, err := root.OpenFile(filepath.FromSlash(f.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(out, h), &contextReader{ctx: ctx, reader: io.LimitReader(reader, f.Size)})
		syncErr := out.Sync()
		closeErr := out.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			return err
		}
		if written != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("librarypack: embedded index checksum mismatch for %s", f.Path)
		}
	}
	if len(seen) != len(want) {
		return errors.New("librarypack: embedded index bundle is incomplete")
	}
	if err := in.Close(); err != nil {
		return err
	}
	return os.Remove(bundlePath)
}
