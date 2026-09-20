package localcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath maps portable path evidence to this machine without allowing a
// relative path or symlink to escape the configured root. A missing/offline
// path does not affect the track's recommendation eligibility.
func (c *Catalog) ResolvePath(ctx context.Context, id string) (PathResolution, error) {
	localID, err := c.localID(id)
	if err != nil {
		return PathResolution{}, err
	}
	if c.provenance.Source == "shared_pack" {
		return PathResolution{TrackID: id, State: PathNoEvidence}, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return PathResolution{}, err
	}
	defer done()
	track, ok, err := generation.Lookup(ctx, localID)
	if err != nil {
		return PathResolution{}, err
	}
	if !ok {
		return PathResolution{}, os.ErrNotExist
	}
	result := PathResolution{TrackID: id, RootAlias: track.RootAlias, RelativePath: track.RelativePath}
	if track.RootAlias == "" || track.RelativePath == "" {
		result.State = PathNoEvidence
		return result, nil
	}
	root, mapped := c.rootMap[track.RootAlias]
	if !mapped {
		result.State = PathUnmapped
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return PathResolution{}, err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		result.State, result.Detail = PathOffline, "mapped root is unavailable"
		return result, nil
	}
	if !rootInfo.IsDir() {
		result.State, result.Detail = PathOffline, "mapped root is not a directory"
		return result, nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		result.State, result.Detail = PathOffline, "mapped root cannot be resolved"
		return result, nil
	}
	candidate := filepath.Join(root, filepath.FromSlash(track.RelativePath))
	if !contained(root, candidate) {
		result.State, result.Detail = PathUnsafe, "portable path escapes mapped root"
		return result, nil
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.State, result.Detail, result.Path = PathMissing, "mapped file is unavailable", candidate
			return result, nil
		}
		result.State, result.Detail = PathUnsafe, "mapped file cannot be resolved safely"
		return result, nil
	}
	if !contained(resolvedRoot, resolvedCandidate) {
		result.State, result.Detail = PathUnsafe, "mapped file resolves outside its root"
		return result, nil
	}
	info, err := os.Stat(resolvedCandidate)
	if err != nil {
		result.State, result.Detail = PathMissing, "mapped file is unavailable"
		return result, nil
	}
	if !info.Mode().IsRegular() {
		result.State, result.Detail = PathUnsafe, "mapped path is not a regular file"
		return result, nil
	}
	result.State, result.Path = PathAvailable, resolvedCandidate
	return result, nil
}

func contained(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
