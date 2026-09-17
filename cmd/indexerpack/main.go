// Command indexerpack creates a reproducible self-extracting playlist-indexer.
package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/indexerbundle"
	"github.com/platten/playlistai/internal/localaudio"
)

type input struct{ prefix, root string }

func main() {
	launcher := flag.String("launcher", "", "unpacked playlist-indexer executable")
	codec := flag.String("codec", "", "verified codec payload directory")
	model := flag.String("model", "", "optional verified MERT bundle directory")
	out := flag.String("out", "", "output executable")
	flag.Parse()
	if err := pack(*launcher, *codec, *model, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func pack(launcher, codec, model, out string) error {
	if launcher == "" || codec == "" || out == "" {
		return errors.New("indexerpack: --launcher, --codec, and --out are required")
	}
	codecAbs, err := filepath.Abs(codec)
	if err != nil {
		return err
	}
	if _, err := localaudio.OpenRuntime(codecAbs); err != nil {
		return fmt.Errorf("indexerpack: invalid codec payload: %w", err)
	}
	inputs := []input{{"codec", codecAbs}}
	if model != "" {
		modelAbs, err := filepath.Abs(model)
		if err != nil {
			return err
		}
		if _, err := audio.ReadMERTBundle(modelAbs); err != nil {
			return fmt.Errorf("indexerpack: invalid MERT payload: %w", err)
		}
		inputs = append(inputs, input{"mert", modelAbs})
	}
	launcherFile, err := os.Open(launcher)
	if err != nil {
		return err
	}
	defer launcherFile.Close()
	info, err := launcherFile.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("indexerpack: launcher is not regular")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".playlist-indexer-pack-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, launcherFile); err != nil {
		tmp.Close()
		return err
	}
	start, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		tmp.Close()
		return err
	}
	zw := zip.NewWriter(tmp)
	for _, source := range inputs {
		if err := addTree(zw, source); err != nil {
			zw.Close()
			tmp.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return err
	}
	end, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(indexerbundle.Trailer(uint64(end - start))); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, out); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(out))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func addTree(zw *zip.Writer, source input) error {
	var paths []string
	err := filepath.WalkDir(source.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source.root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("indexerpack: payload symlinks are forbidden")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("indexerpack: payload files must be regular")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, err := filepath.Rel(source.root, path)
		if err != nil {
			return err
		}
		name := source.prefix + "/" + filepath.ToSlash(rel)
		if !fs.ValidPath(name) || strings.Contains(name, "\\") {
			return errors.New("indexerpack: invalid payload path")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name, header.Method = name, zip.Store
		header.Modified = time.Unix(0, 0).UTC()
		header.SetMode(info.Mode().Perm())
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}
