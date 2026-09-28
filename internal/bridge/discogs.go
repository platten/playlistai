package bridge

import (
	"context"

	"github.com/platten/playlistai/internal/app"
)

func (a *API) GetDiscogsStatus(ctx context.Context) (app.DiscogsStatus, error) {
	return a.app.GetDiscogsStatus(ctx)
}
func (a *API) InstallDiscogs(ctx context.Context) error {
	ctx, _, finish := a.operations.begin(ctx, "discogs-install")
	defer finish()
	a.cancelRecommendationWork()
	return a.app.InstallDiscogs(ctx, NewWailsProgress())
}
func (a *API) RemoveDiscogs() error {
	a.cancelRecommendationWork()
	a.operations.cancel("discogs-install")
	return a.app.RemoveDiscogs()
}
