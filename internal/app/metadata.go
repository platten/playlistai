package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/metadata"
	"github.com/platten/playlistai/internal/ports"
)

type MetadataBundleInfo struct {
	Configured            bool   `json:"configured"`
	Installed             bool   `json:"installed"`
	CatalogReady          bool   `json:"catalogReady"`
	Date                  string `json:"date"`
	Tracks                int64  `json:"tracks"`
	MusicBrainzConfigured bool   `json:"musicBrainzConfigured"`
	MusicBrainzInstalled  bool   `json:"musicBrainzInstalled"`
	MusicBrainzSnapshot   string `json:"musicBrainzSnapshot"`
	MusicBrainzRecordings int64  `json:"musicBrainzRecordings"`
}

func (c *Container) GetMetadataBundleInfo() MetadataBundleInfo {
	i := MetadataBundleInfo{Configured: c.cfg.Metadata.ManifestURL != "", CatalogReady: c.Runtime().Resolver != nil}
	s, err := metadata.Open(metadata.ActivePath(filepath.Join(c.cfg.DataDir, "metadata")))
	if err == nil {
		defer s.Close()
		i.Date = s.Info().Date
		i.Tracks = s.Info().Tracks
		i.Installed = c.Runtime().Resolver != nil && s.Compatible(c.Runtime().Resolver.CatalogVersion())
	}
	i.MusicBrainzConfigured = c.cfg.Metadata.MusicBrainzManifestURL != ""
	if store, openErr := mbindex.Open(mbindex.ActivePath(filepath.Join(c.cfg.DataDir, "musicbrainz-metadata"))); openErr == nil {
		defer store.Close()
		info := store.Info()
		i.MusicBrainzInstalled = true
		i.MusicBrainzSnapshot = info.Snapshot
		i.MusicBrainzRecordings = info.Recordings
	}
	return i
}

func (c *Container) InstallMetadataBundle(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.metadataInstallMu.Lock()
	defer c.metadataInstallMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Runtime().Resolver == nil {
		return errors.New("download the recommendation catalog first")
	}
	if c.cfg.Metadata.ManifestURL == "" {
		return errors.New("no hosted metadata bundle is configured")
	}
	service, ok := c.Knowledge.(interface{ ActivateDataset(string) error })
	if !ok {
		return errors.New("music metadata service unavailable")
	}
	path, err := metadata.Install(ctx, c.cfg.Metadata.ManifestURL, filepath.Join(c.cfg.DataDir, "metadata"), c.Runtime().Resolver.CatalogVersion(), p)
	if err != nil {
		return err
	}
	return service.ActivateDataset(path)
}

func (c *Container) InstallMusicBrainzBundle(ctx context.Context, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	c.metadataInstallMu.Lock()
	defer c.metadataInstallMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.cfg.Metadata.MusicBrainzManifestURL == "" {
		return errors.New("no hosted MusicBrainz bundle is configured")
	}
	service, ok := c.Knowledge.(interface{ ActivateOfflineIndex(string) error })
	if !ok {
		return errors.New("music metadata service unavailable")
	}
	path, err := mbindex.Install(ctx, c.cfg.Metadata.MusicBrainzManifestURL, filepath.Join(c.cfg.DataDir, "musicbrainz-metadata"), p)
	if err != nil {
		return err
	}
	return service.ActivateOfflineIndex(path)
}
