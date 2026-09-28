package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
)

// additionalPreparedBytes counts retained graph snapshots and the dedicated
// specialist/prepared-pack roots. Existing base catalogs, personal libraries,
// CLAP and MERT are outside this explicitly additional installed-data budget.
// Temporary downloads are excluded; callers reserve their final expanded size.
func (c *Container) additionalPreparedBytes(ctx context.Context) (int64, error) {
	var total int64
	for _, name := range []string{"music-graph", "music-classifiers", "prepared-music-packs"} {
		root := filepath.Join(c.cfg.DataDir, name)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if errors.Is(walkErr, os.ErrNotExist) && path == root {
				return fs.SkipDir
			}
			if walkErr != nil {
				return walkErr
			}
			if path == root && !entry.IsDir() {
				return errors.New("prepared-data root must be a directory")
			}
			if entry.IsDir() {
				if path != root && strings.HasPrefix(entry.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if name == "music-graph" && (!strings.HasPrefix(entry.Name(), "graph-") || !strings.HasSuffix(entry.Name(), ".json")) {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") {
				return nil
			}
			if !entry.Type().IsRegular() {
				return errors.New("nonregular installed prepared-data asset")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if err = musicgraph.CheckAdditionalDataBudget(total, info.Size()); err != nil {
				return err
			}
			total += info.Size()
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	for _, name := range []string{"discovery-data", "local-library"} {
		root := filepath.Join(c.cfg.DataDir, name)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if errors.Is(walkErr, os.ErrNotExist) && path == root {
				return fs.SkipDir
			}
			if walkErr != nil {
				return walkErr
			}
			if path == root && !entry.IsDir() {
				return errors.New("prepared catalog root must be a directory")
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "local-index-v3" || entry.Name() == "downloads" || entry.Name() == "transport-cache" {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Name() != librarypack.ManifestName {
				return nil
			}
			if !entry.Type().IsRegular() {
				return errors.New("nonregular prepared catalog manifest")
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			raw, readErr := io.ReadAll(io.LimitReader(f, 4<<20+1))
			closeErr := f.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if len(raw) > 4<<20 {
				return errors.New("oversized prepared catalog manifest")
			}
			var manifest librarypack.Manifest
			if err = json.Unmarshal(raw, &manifest); err != nil {
				return err
			}
			if manifest.Format != librarypack.Format || manifest.Coverage.Classifier == 0 {
				return nil
			}
			if err = manifest.Validate(librarypack.Limits{}); err != nil {
				return err
			}
			// The importer verifies these member sizes and hashes before invoking
			// this guard. indexes.tar is transient and removed during staging.
			sizes := []int64{int64(len(raw))}
			for _, file := range manifest.Files {
				if file.Name != librarypack.IndexBundleName {
					sizes = append(sizes, file.Size)
				}
			}
			for _, file := range manifest.IndexFiles {
				sizes = append(sizes, file.Size)
			}
			if err = musicgraph.CheckAdditionalDataBudget(total, sizes...); err != nil {
				return err
			}
			for _, size := range sizes {
				total += size
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, ctx.Err()
}

// preparedInstallGuard serializes the budget check and publication with graph
// imports and other classified-pack activations across application processes.
// Staged verified generations already reside under the scanned roots, so the
// check includes their final installed bytes before an active pointer changes.
func (c *Container) preparedInstallGuard(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := c.graphInstallLock()
	if err != nil {
		return nil, err
	}
	if _, err = c.additionalPreparedBytes(ctx); err != nil {
		_ = release()
		return nil, err
	}
	return release, nil
}

func (c *Container) checkGraphInstalledBudget(ctx context.Context, hash string, size int64) error {
	if _, err := os.Lstat(filepath.Join(c.graphDir(), graphName(hash))); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	existing, err := c.additionalPreparedBytes(ctx)
	if err != nil {
		return err
	}
	return musicgraph.CheckAdditionalDataBudget(existing, size)
}
