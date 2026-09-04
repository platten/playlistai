package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// Enricher resolves cross-service metadata (ISRC, album, year, all artists) for
// a set of tracks, typically by querying MusicBrainz on artist + title.
//
// It must never fail the whole batch because one track did not match: unmatched
// tracks come back with Matched == false and zero metadata. It reports progress
// roughly once per resolved track (MusicBrainz allows ~1 request/second).
type Enricher interface {
	Enrich(ctx context.Context, refs []core.TrackRef, p Progress) ([]core.EnrichedTrack, error)
	Name() string
}
