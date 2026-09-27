package librarypack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// ArtistMBIDs reads recording artist credits only. Album artists, display names,
// and fuzzy name matches cannot establish a recording's performer identity.
func ArtistMBIDs(raw json.RawMessage) []string {
	var tags map[string]json.RawMessage
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for key, value := range tags {
		key = strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(key)))
		if key != "musicbrainz_artistid" {
			continue
		}
		var scalar string
		var values []string
		if json.Unmarshal(value, &scalar) == nil {
			values = []string{scalar}
		} else if json.Unmarshal(value, &values) != nil {
			continue
		}
		for _, value := range values {
			for _, token := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' || unicode.IsSpace(r) }) {
				if id := CanonicalMBID(token); id != "" && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// ArtistRecordingsByMBID indexes immutable recording credits once per generation.
// Building a derivative in memory keeps existing verified packs compatible and
// never modifies their signed metadata database. Cancellation leaves no partial
// index cached, so a later request can retry.
func (g *Generation) ArtistRecordingsByMBID(ctx context.Context, mbid string, limit int) ([]Track, error) {
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
	g.artistIndexMu.Lock()
	index := g.artistIndex
	g.artistIndexMu.Unlock()
	if index == nil {
		// Build outside the lock: another canceled request must not wait on a
		// long first scan. Concurrent first requests may duplicate this one scan.
		built, err := g.buildArtistIndex(ctx)
		if err != nil {
			return nil, err
		}
		g.artistIndexMu.Lock()
		if g.artistIndex == nil {
			g.artistIndex = built
		}
		index = g.artistIndex
		g.artistIndexMu.Unlock()
	}
	ids := index[mbid]
	var out []Track
	for _, id := range ids[:min(len(ids), min(limit, 512))] {
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

func (g *Generation) buildArtistIndex(ctx context.Context) (map[string][]string, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT id,raw_tags_json FROM tracks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := map[string][]string{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		for _, artist := range ArtistMBIDs(raw) {
			index[artist] = append(index[artist], id)
		}
	}
	return index, rows.Err()
}
