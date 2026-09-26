package main

import (
	"archive/tar"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ulikunitz/xz"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/mbindex"
)

// The fixture contains synthetic identity records, not a musical-quality set.
//
//go:embed testdata/context-recognition-v1/*.jsonl
var recognitionFixtureFiles embed.FS

const evaluationMarker = ".intenteval-isolated"

func prepareApplicationData(ctx context.Context, path string, fixture bool) (*app.Container, error) {
	root, err := isolatedIntentDataDirectory(path)
	if err != nil {
		return nil, err
	}
	// Refuse existing runtime settings rather than overriding a copied user's
	// preferences. This preparation container must never start another model.
	prefs, err := config.LoadPrefsChecked(root)
	if err != nil {
		return nil, err
	}
	if prefs.ModelPath != "" && !prefs.ModelDisabled {
		return nil, fmt.Errorf("isolated recognition data must not configure a model")
	}
	for _, bundle := range []string{"music-analysis", "mert-analysis"} {
		if _, err := os.Stat(filepath.Join(root, bundle, "active.json")); err == nil {
			return nil, fmt.Errorf("isolated recognition data must not configure %s workers", bundle)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if fixture {
		if err := installRecognitionFixture(ctx, root); err != nil {
			return nil, err
		}
	}
	cfg := config.Default()
	cfg.DataDir = root
	cfg.Catalog.Dir = filepath.Join(root, "catalog")
	cfg.Enrich.CachePath = filepath.Join(root, "evaluation-metadata.sqlite")
	cfg.Preview.Provider = config.PreviewOff
	cfg.Metadata.MusicBrainzManifestURL, cfg.Discovery.ManifestURL = "", ""
	cfg.Catalog.ArchiveURL, cfg.Catalog.ManifestURL = "", ""
	return app.New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func isolatedIntentDataDirectory(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	defaultRoot, err := filepath.Abs(config.Default().DataDir)
	if err != nil {
		return "", err
	}
	if root == defaultRoot || filepath.Dir(root) == root {
		return "", fmt.Errorf("recognition preparation requires isolated evaluation data")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	defaultResolved, _ := filepath.EvalSymlinks(defaultRoot)
	if root != resolved || root == defaultResolved {
		return "", fmt.Errorf("isolated evaluation data must not link to another directory")
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("isolated evaluation data contains a symlink: %s", path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	marker := filepath.Join(root, evaluationMarker)
	if len(entries) == 0 {
		if err := os.WriteFile(marker, []byte("intenteval isolated data v1\n"), 0600); err != nil {
			return "", err
		}
	} else if raw, err := os.ReadFile(marker); err != nil || string(raw) != "intenteval isolated data v1\n" {
		return "", fmt.Errorf("existing data lacks the %s marker; use a new empty directory", evaluationMarker)
	}
	return root, nil
}

func installRecognitionFixture(ctx context.Context, root string) error {
	directory := filepath.Join(root, "musicbrainz-metadata")
	index := mbindex.ActivePath(directory)
	if _, err := os.Stat(index); err == nil {
		store, err := mbindex.Open(index)
		if err != nil {
			return err
		}
		defer store.Close()
		if store.SnapshotIdentity().Snapshot != "20260925-000000" {
			return fmt.Errorf("recognition fixture cannot replace an installed snapshot")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(root, ".recognition-fixture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, entity := range []string{"artist", "recording"} {
		raw, err := recognitionFixtureFiles.ReadFile("testdata/context-recognition-v1/" + entity + ".jsonl")
		if err != nil {
			return err
		}
		if err := writeFixtureArchive(filepath.Join(stage, entity+".tar.xz"), entity, raw); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	_, err = mbindex.Build(ctx, mbindex.BuildOptions{Output: index, Snapshot: "20260925-000000", ArtistArchive: filepath.Join(stage, "artist.tar.xz"), RecordingArchive: filepath.Join(stage, "recording.tar.xz"), SQLiteCacheMiB: 1})
	return err
}

func writeFixtureArchive(path, entity string, raw []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := xz.NewWriter(file)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "mbdump/" + entity, Mode: 0600, Size: int64(len(raw))}); err != nil {
		return err
	}
	if _, err := archive.Write(raw); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
		return err
	}
	return file.Close()
}
