package bridge

import (
	"context"

	"github.com/platten/playlistai/internal/credentials"
)

// Connection status is deliberately separate from credential input. Stored
// tokens never cross the bridge, even after a connection has been replaced.
func (a *API) GetListenBrainzStatus() credentials.Status { return a.app.ReadListenBrainzStatus() }
func (a *API) ConnectListenBrainz(ctx context.Context, token string) (credentials.Status, error) {
	ctx, current, finish := a.operations.begin(ctx, "listenbrainz-connection")
	defer finish()
	status, err := a.app.ConnectListenBrainz(ctx, token)
	if err == nil && !current() {
		return status, context.Canceled
	}
	return status, err
}
func (a *API) DisconnectListenBrainz(ctx context.Context) (credentials.Status, error) {
	_, _, finish := a.operations.begin(ctx, "listenbrainz-connection")
	defer finish()
	return a.app.DisconnectListenBrainz()
}
