// Package spotifycdn implements ports.PreviewProvider over nothing but the
// preview URL already bundled in the catalog (a Spotify CDN link captured when
// the Deej-AI dataset was built). No network, no API key, no cache — it is a
// pure passthrough, used when preview.provider = "spotify" to avoid any runtime
// network call.
package spotifycdn

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Provider implements ports.PreviewProvider.
type Provider struct{}

// New returns a bundled-URL-only preview provider.
func New() *Provider { return &Provider{} }

// Name implements ports.PreviewProvider.
func (*Provider) Name() string { return "spotifycdn" }

// PreviewURL returns bundledURL unchanged. Many catalog rows carry no bundled
// URL at all — that is a normal miss, not an error.
func (*Provider) PreviewURL(_ context.Context, _ core.TrackRef, bundledURL string) (string, bool, error) {
	if bundledURL == "" {
		return "", false, nil
	}
	return bundledURL, true, nil
}

var _ ports.PreviewProvider = (*Provider)(nil)
