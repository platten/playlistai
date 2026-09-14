package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
)

type MetadataBundleInfo struct {
	MusicBrainzConfigured bool   `json:"musicBrainzConfigured"`
	MusicBrainzInstalled  bool   `json:"musicBrainzInstalled"`
	MusicBrainzSnapshot   string `json:"musicBrainzSnapshot"`
	MusicBrainzRecordings int64  `json:"musicBrainzRecordings"`
}

func (c *Container) GetMetadataBundleInfo() MetadataBundleInfo {
	i := MetadataBundleInfo{MusicBrainzConfigured: c.cfg.Metadata.MusicBrainzManifestURL != ""}
	if store, openErr := mbindex.Open(mbindex.ActivePath(filepath.Join(c.cfg.DataDir, "musicbrainz-metadata"))); openErr == nil {
		defer store.Close()
		info := store.Info()
		i.MusicBrainzInstalled = true
		i.MusicBrainzSnapshot = info.Snapshot
		i.MusicBrainzRecordings = info.Recordings
	}
	return i
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
