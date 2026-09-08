package bridge

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/enrich/musicbrainz"
)

type metadataService interface {
	ClearCache(context.Context) error
	MetadataStatus() musicbrainz.MetadataStatus
	SetDiscogsToken(string) error
}

func (a *API) GetMetadataStatus() (musicbrainz.MetadataStatus, error) {
	service, ok := a.app.Knowledge.(metadataService)
	if !ok {
		return musicbrainz.MetadataStatus{}, errors.New("music metadata service unavailable")
	}
	return service.MetadataStatus(), nil
}

func (a *API) SetDiscogsToken(token string) error {
	service, ok := a.app.Knowledge.(metadataService)
	if !ok {
		return errors.New("music metadata service unavailable")
	}
	return service.SetDiscogsToken(token)
}

// ClearMusicMetadataCache is deliberately distinct from ClearAnalysis and
// ClearTasteData. Saved playlists/evidence snapshots are not deleted.
func (a *API) ClearMusicMetadataCache(ctx context.Context) error {
	service, ok := a.app.Knowledge.(metadataService)
	if !ok {
		return errors.New("music metadata service unavailable")
	}
	a.operations.cancel("intent-preview")
	a.operations.cancel("prompt-generation")
	a.operations.cancel("playlist-build")
	if err := service.ClearCache(ctx); err != nil {
		return err
	}
	a.intentCache.clear()
	return nil
}
