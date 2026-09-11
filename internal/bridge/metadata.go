package bridge

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/app"

	"github.com/platten/playlistai/internal/enrich/musicbrainz"
)

func (a *API) GetMetadataBundleInfo() app.MetadataBundleInfo { return a.app.GetMetadataBundleInfo() }

func (a *API) InstallMetadataBundle(ctx context.Context) error {
	ctx, _, finish := a.operations.begin(ctx, "metadata-install")
	defer finish()
	a.cancelRecommendationWork()
	err := a.app.InstallMetadataBundle(ctx, NewWailsProgress())
	if err == nil {
		a.intentCache.clear()
	}
	return err
}

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
	a.cancelRecommendationWork()
	if err := service.ClearCache(ctx); err != nil {
		return err
	}
	a.intentCache.clear()
	return nil
}
