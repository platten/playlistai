package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
)

type MetadataBundleInfo struct {
	MusicBrainzConfigured    bool   `json:"musicBrainzConfigured"`
	MusicBrainzInstalled     bool   `json:"musicBrainzInstalled"`
	MusicBrainzSnapshot      string `json:"musicBrainzSnapshot"`
	MusicBrainzRecordings    int64  `json:"musicBrainzRecordings"`
	GenreVocabularyInstalled bool   `json:"genreVocabularyInstalled"`
	GenreVocabularyHash      string `json:"genreVocabularyHash"`
}

func (c *Container) GetMetadataBundleInfo() MetadataBundleInfo {
	dir := filepath.Join(c.cfg.DataDir, "musicbrainz-metadata")
	i := MetadataBundleInfo{MusicBrainzConfigured: c.cfg.Metadata.MusicBrainzManifestURL != "", GenreVocabularyHash: mbindex.ActiveGenreHash(dir)}
	i.GenreVocabularyInstalled = i.GenreVocabularyHash != ""
	if store, openErr := mbindex.Open(mbindex.ActivePath(dir)); openErr == nil {
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
