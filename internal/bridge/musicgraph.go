package bridge

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/app"
)

type PreparedMusicStatus struct {
	Configured bool                 `json:"configured"`
	Data       app.MusicGraphStatus `json:"data"`
}

func (a *API) GetPreparedMusicStatus(ctx context.Context) (PreparedMusicStatus, error) {
	data, err := a.app.GetMusicGraphStatus(ctx)
	return PreparedMusicStatus{Configured: a.app.Config().Metadata.MusicGraphManifestURL != "", Data: data}, err
}

func (a *API) UpdatePreparedMusicData(ctx context.Context) (PreparedMusicStatus, error) {
	source := a.app.Config().Metadata.MusicGraphManifestURL
	if source == "" {
		return PreparedMusicStatus{}, errors.New("no prepared music data release is configured")
	}
	ctx, current, finish := a.operations.begin(ctx, "music-graph")
	defer finish()
	_, err := a.app.InstallMusicGraph(ctx, source, NewWailsProgress())
	if err != nil {
		return PreparedMusicStatus{}, err
	}
	if !current() {
		return PreparedMusicStatus{}, context.Canceled
	}
	a.intentCache.clear()
	return a.GetPreparedMusicStatus(ctx)
}
