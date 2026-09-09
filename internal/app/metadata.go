package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/platten/playlistai/internal/metadata"
	"github.com/platten/playlistai/internal/ports"
)

type MetadataBundleInfo struct {
	Configured   bool   `json:"configured"`
	Installed    bool   `json:"installed"`
	CatalogReady bool   `json:"catalogReady"`
	Date         string `json:"date"`
	Tracks       int64  `json:"tracks"`
}

func (c *Container) GetMetadataBundleInfo() MetadataBundleInfo {
	i := MetadataBundleInfo{Configured: c.cfg.Metadata.ManifestURL != "", CatalogReady: c.Resolver != nil}
	s, err := metadata.Open(metadata.ActivePath(filepath.Join(c.cfg.DataDir, "metadata")))
	if err == nil {
		defer s.Close()
		i.Date = s.Info().Date
		i.Tracks = s.Info().Tracks
		i.Installed = c.Resolver != nil && s.Compatible(c.Resolver.CatalogVersion())
	}
	return i
}

func (c *Container) InstallMetadataBundle(ctx context.Context, p ports.Progress) error {
	c.metadataInstallMu.Lock()
	defer c.metadataInstallMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Resolver == nil {
		return errors.New("download the recommendation catalog first")
	}
	if c.cfg.Metadata.ManifestURL == "" {
		return errors.New("no hosted metadata bundle is configured")
	}
	service, ok := c.Knowledge.(interface{ ActivateDataset(string) error })
	if !ok {
		return errors.New("music metadata service unavailable")
	}
	path, err := metadata.Install(ctx, c.cfg.Metadata.ManifestURL, filepath.Join(c.cfg.DataDir, "metadata"), c.Resolver.CatalogVersion(), p)
	if err != nil {
		return err
	}
	return service.ActivateDataset(path)
}
