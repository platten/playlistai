package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// RecordingIdentityCatalog joins public recording identities to installed
// catalog members. Implementations use indexed IDs, never fuzzy title matches.
type RecordingIdentityCatalog interface {
	RecordingsByMBID(context.Context, string, int) ([]core.TrackRef, error)
}
