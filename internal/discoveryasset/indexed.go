package discoveryasset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
)

// BuildIndexedFromPack is the offline packaging step shared by playlist-indexer
// and the curated-release builder. It never changes the input pack or the
// destination until every search/profile index is complete and verified.
func BuildIndexedFromPack(ctx context.Context, source, output string, limits librarypack.Limits) (librarypack.Manifest, error) {
	declared, err := inspectPackManifestWithLimits(ctx, source, limits)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	if declared.Version != librarypack.FormatVersion {
		return librarypack.Manifest{}, errors.New("discoveryasset: indexed export requires a version-7 source pack; re-export from playlist-indexer state")
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return librarypack.Manifest{}, err
	}
	work, err := os.MkdirTemp(filepath.Dir(out), ".paipack-indexing-*")
	if err != nil {
		return librarypack.Manifest{}, err
	}
	defer os.RemoveAll(work)
	manager, err := librarypack.OpenManager(ctx, filepath.Join(work, "staging"), limits)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, source)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	defer func() { _ = manager.Discard(staged) }()
	if err := localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 2, ShardRows: 16_384, MaxScratchBytes: 256 << 20}); err != nil {
		return librarypack.Manifest{}, fmt.Errorf("build portable search indexes: %w", err)
	}
	if err := BuildEmbeddedCompanion(ctx, staged.Generation()); err != nil {
		return librarypack.Manifest{}, fmt.Errorf("build portable discovery profiles: %w", err)
	}
	return librarypack.WriteIndexed(ctx, out, staged.Generation(), limits)
}
