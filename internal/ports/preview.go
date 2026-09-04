package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// PreviewProvider resolves a playable ~30s preview URL for a track without
// requiring any API key. Implementations: deezer (public search API) and
// spotifycdn (the preview URL bundled in the catalog). A miss (ok == false) is
// normal — many tracks have no preview anywhere — and must not be surfaced as an
// error.
type PreviewProvider interface {
	PreviewURL(ctx context.Context, ref core.TrackRef, bundledURL string) (url string, ok bool, err error)
	Name() string
}
