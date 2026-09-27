package localcatalog

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func (c *Catalog) RecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	tracks, err := generation.RecordingsByMBID(ctx, mbid, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.TrackRef, 0, len(tracks))
	for _, track := range tracks {
		t := c.convertTrack(track)
		out = append(out, core.TrackRef{ID: t.ID, Artist: t.Artist, Title: t.Title, RecordingIdentity: t.RecordingIdentity})
	}
	return out, ctx.Err()
}

func (c *CompositeCatalog) RecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	if limit <= 0 {
		return nil, ctx.Err()
	}
	limit = min(limit, 64)
	out, err := c.local.RecordingsByMBID(ctx, mbid, limit)
	if err != nil {
		return nil, err
	}
	if len(out) < limit && c.mode != ModeLibraryOnly {
		if base, ok := c.base.(ports.RecordingIdentityCatalog); ok {
			more, err := base.RecordingsByMBID(ctx, mbid, limit-len(out))
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
	}
	return out, ctx.Err()
}

func (c *Catalog) ArtistRecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	tracks, err := generation.ArtistRecordingsByMBID(ctx, mbid, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.TrackRef, 0, len(tracks))
	for _, track := range tracks {
		t := c.convertTrack(track)
		out = append(out, core.TrackRef{ID: t.ID, Artist: t.Artist, Title: t.Title, RecordingIdentity: t.RecordingIdentity})
	}
	return out, ctx.Err()
}

func (c *CompositeCatalog) ArtistRecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	if limit <= 0 {
		return nil, ctx.Err()
	}
	limit = min(limit, 512)
	out, err := c.local.ArtistRecordingsByMBID(ctx, mbid, limit)
	if err != nil {
		return nil, err
	}
	if len(out) < limit && c.mode != ModeLibraryOnly {
		if base, ok := c.base.(ports.ArtistIdentityCatalog); ok {
			more, err := base.ArtistRecordingsByMBID(ctx, mbid, limit-len(out))
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
	}
	return out, ctx.Err()
}
