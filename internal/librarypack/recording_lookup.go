package librarypack

import (
	"context"
	"errors"
)

// RecordingsByMBID uses the existing recording index without loading vectors.
func (g *Generation) RecordingsByMBID(ctx context.Context, mbid string, limit int) ([]Track, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mbid = CanonicalMBID(mbid)
	if mbid == "" || limit <= 0 {
		return nil, nil
	}
	if g == nil || g.db == nil {
		return nil, errors.New("librarypack: generation is closed")
	}
	rows, err := g.db.QueryContext(ctx, `SELECT id FROM tracks WHERE musicbrainz_recording<>'' AND musicbrainz_recording=? COLLATE NOCASE ORDER BY id LIMIT ?`, mbid, min(limit, 64))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	var out []Track
	for _, id := range ids {
		track, ok, err := g.Lookup(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, track)
		}
	}
	return out, ctx.Err()
}
