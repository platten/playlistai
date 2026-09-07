package audio

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func safeArchiveMember(name string) bool {
	name = strings.TrimPrefix(name, "./")
	return name != "" && !strings.ContainsAny(name, "\\:") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

// Only the pinned regular library member is extracted, never archive paths or links.
func unpackRuntime(ctx context.Context, dir string, artifact BundleArtifact) error {
	if !safeArchiveMember(artifact.ArchiveMember) {
		return fmt.Errorf("audio: unsafe runtime member")
	}
	write := func(reader io.Reader, size int64) error {
		if size != artifact.UnpackedSize {
			return fmt.Errorf("audio: runtime size mismatch")
		}
		target := filepath.Join(dir, filepath.Base(artifact.ArchiveMember))
		file, err := os.CreateTemp(dir, ".runtime-*")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx: ctx, reader: reader}, size+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size || hex.EncodeToString(hash.Sum(nil)) != artifact.UnpackedSHA256 {
			return fmt.Errorf("audio: extracted runtime integrity mismatch")
		}
		return os.Rename(file.Name(), target)
	}
	archive := filepath.Join(dir, artifact.Name)
	if strings.HasSuffix(artifact.Name, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, member := range z.File {
			if member.Name != artifact.ArchiveMember {
				continue
			}
			if !member.Mode().IsRegular() || member.UncompressedSize64 != uint64(artifact.UnpackedSize) {
				return fmt.Errorf("audio: invalid zipped runtime")
			}
			reader, err := member.Open()
			if err != nil {
				return err
			}
			defer reader.Close()
			return write(reader, artifact.UnpackedSize)
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(&contextReader{ctx: ctx, reader: gz})
		for {
			member, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if member.Name != artifact.ArchiveMember {
				continue
			}
			if member.Typeflag != tar.TypeReg {
				return fmt.Errorf("audio: runtime member must be a regular file")
			}
			return write(tr, member.Size)
		}
	}
	return fmt.Errorf("audio: pinned runtime library missing from archive")
}
