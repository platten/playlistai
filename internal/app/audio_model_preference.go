package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/platten/playlistai/internal/audio"
)

type clapCandidate struct {
	dir      string
	manifest audio.BundleManifest
}

type mertCandidate struct {
	dir      string
	manifest audio.MERTBundleManifest
}

func (s *analysisState) managers() []*audio.BundleManager {
	if s.alternate == nil {
		return []*audio.BundleManager{s.bundles}
	}
	return []*audio.BundleManager{s.bundles, s.alternate}
}

func (e *enhancedState) managers() []*audio.MERTBundleManager {
	if e.alternate == nil {
		return []*audio.MERTBundleManager{e.bundles}
	}
	return []*audio.MERTBundleManager{e.bundles, e.alternate}
}

func hasActiveMarker(directory string) bool {
	_, err := os.Stat(filepath.Join(directory, "active.json"))
	return err == nil
}

func (s *analysisState) hasInstalledBundle() bool {
	for _, manager := range s.managers() {
		if manager != nil && hasActiveMarker(manager.Directory) {
			return true
		}
	}
	return false
}

func (e *enhancedState) hasInstalledBundle() bool {
	for _, manager := range e.managers() {
		if manager != nil && hasActiveMarker(manager.Directory) {
			return true
		}
	}
	return false
}

func (s *analysisState) installedBundles(ctx context.Context, cudaAvailable bool) []clapCandidate {
	var found []clapCandidate
	for _, manager := range s.managers() {
		if manager == nil {
			continue
		}
		dir, manifest, err := manager.ActiveStartupContext(ctx)
		if err == nil && (manifest.Backend() != "cuda" || cudaAvailable) {
			found = append(found, clapCandidate{dir, manifest})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return found[i].manifest.Backend() == "cuda" && found[j].manifest.Backend() != "cuda"
	})
	return found
}

func (e *enhancedState) installedBundles(ctx context.Context, cudaAvailable bool) []mertCandidate {
	var found []mertCandidate
	for _, manager := range e.managers() {
		if manager == nil {
			continue
		}
		dir, manifest, err := manager.ActiveStartupContext(ctx)
		if err == nil && (manifest.Backend() != "cuda" || cudaAvailable) {
			found = append(found, mertCandidate{dir, manifest})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return found[i].manifest.Backend() == "cuda" && found[j].manifest.Backend() != "cuda"
	})
	return found
}

func (s *analysisState) managerFor(ctx context.Context, backend string) (*audio.BundleManager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var empty *audio.BundleManager
	var broken *audio.BundleManager
	for _, manager := range s.managers() {
		if manager == nil {
			continue
		}
		_, manifest, err := manager.ActiveStartupContext(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) {
			if empty == nil {
				empty = manager
			}
			continue
		}
		if err != nil {
			if broken == nil {
				broken = manager
			}
			continue
		}
		if manifest.Backend() == backend {
			return manager, nil
		}
	}
	if empty != nil {
		return empty, nil
	}
	if broken != nil {
		return broken, nil
	}
	return nil, fmt.Errorf("no available %s CLAP slot", backend)
}

func (e *enhancedState) managerFor(ctx context.Context, backend string) (*audio.MERTBundleManager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var empty *audio.MERTBundleManager
	var broken *audio.MERTBundleManager
	for _, manager := range e.managers() {
		if manager == nil {
			continue
		}
		_, manifest, err := manager.ActiveStartupContext(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) {
			if empty == nil {
				empty = manager
			}
			continue
		}
		if err != nil {
			if broken == nil {
				broken = manager
			}
			continue
		}
		if manifest.Backend() == backend {
			return manager, nil
		}
	}
	if empty != nil {
		return empty, nil
	}
	if broken != nil {
		return broken, nil
	}
	return nil, fmt.Errorf("no available %s MERT slot", backend)
}

// The offline indexer keeps its verified CUDA bundles here. Prefer them when
// available; the normal hosted CPU packs remain the portable fallback.
func indexerOfflineCache() string {
	if root := os.Getenv("PLAYLIST_INDEXER_OFFLINE_CACHE_DIR"); root != "" {
		absolute, err := filepath.Abs(root)
		if err == nil {
			return absolute
		}
		return ""
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "playlist-ai", "indexer-offline")
}

func preferredCUDACLAP() (string, audio.BundleManifest, bool) {
	if !audio.NativeInferenceAvailable() || !audio.MERTCUDAHostAvailable() || indexerOfflineCache() == "" {
		return "", audio.BundleManifest{}, false
	}
	dir := filepath.Join(indexerOfflineCache(), "clap-cuda")
	m, err := audio.ReadStartupBundleContext(context.Background(), dir)
	return dir, m, err == nil && m.Validate() == nil && m.Backend() == "cuda"
}

func preferredCUDAMERT() (string, audio.MERTBundleManifest, bool) {
	if !audio.NativeInferenceAvailable() || !audio.MERTCUDAHostAvailable() || indexerOfflineCache() == "" {
		return "", audio.MERTBundleManifest{}, false
	}
	dir := filepath.Join(indexerOfflineCache(), "mert-cuda")
	m, err := audio.ReadStartupMERTBundleContext(context.Background(), dir)
	return dir, m, err == nil && m.Backend() == "cuda"
}
