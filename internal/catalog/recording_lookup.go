package catalog

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func (d *DynamicCatalog) RecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mbid = librarypack.CanonicalMBID(mbid)
	if mbid == "" || limit <= 0 {
		return nil, nil
	}
	limit = min(limit, 64)
	var out []core.TrackRef
	if base, ok := d.base.(ports.RecordingIdentityCatalog); ok {
		var err error
		out, err = base.RecordingsByMBID(ctx, mbid, limit)
		if err != nil {
			return nil, err
		}
	}
	if len(out) < limit {
		if meta, ok := d.MetaContext(ctx, "musicbrainz:"+mbid); ok && meta.MusicBrainzRecording == mbid {
			out = append(out, meta.Ref)
		}
	}
	return out, ctx.Err()
}

// ArtistRecordingsByMBID preserves the prepared/local credit index through the
// dynamic overlay. Dynamic discoveries without authenticated artist credits do
// not become seeds merely because their display name matches.
func (d *DynamicCatalog) ArtistRecordingsByMBID(ctx context.Context, mbid string, limit int) ([]core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if base, ok := d.base.(ports.ArtistIdentityCatalog); ok {
		return base.ArtistRecordingsByMBID(ctx, mbid, limit)
	}
	return nil, nil
}
