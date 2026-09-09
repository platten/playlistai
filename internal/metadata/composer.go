package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// ComposerVersion is an optional additive extension to both index formats.
// Older indexes remain readable and return no composer evidence until rebuilt.
const ComposerVersion = "discogs-composers/v1"

type composerXMLCredit struct {
	ID     int64  `xml:"id"`
	Name   string `xml:"name"`
	ANV    string `xml:"anv"`
	Role   string `xml:"role"`
	Tracks string `xml:"tracks"`
}

type preparedComposer struct{ trackID, value string }

// composerRoles splits only outside annotations: commas inside [ ... ] do not
// introduce another role. Generic Written-By, lyricists and arrangers are not
// promoted to composers; only explicit Composed By / Music By evidence counts.
func composerRoles(raw string) []string {
	var out []string
	depth, start := 0, 0
	add := func(role string) {
		role = strings.TrimSpace(role)
		base, _, _ := strings.Cut(role, "[")
		switch strings.ToLower(strings.TrimSpace(base)) {
		case "composed by", "music by":
			out = append(out, role)
		}
	}
	for i, c := range raw {
		switch c {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				return nil
			}
			depth--
		case ',':
			if depth == 0 {
				add(raw[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil
	}
	add(raw[start:])
	return out
}

// scopedPositions resolves publisher comma lists / "to" ranges using actual
// tracklist order, never numeric guesses (A1, B1, 1-1, etc.). Missing, duplicate
// or reversed endpoints make the entire expression unknown rather than broad.
func scopedPositions(raw string, tracks []xmlTrack) (map[int]bool, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	positions := map[string]int{}
	for i, t := range tracks {
		p := strings.TrimSpace(t.Position)
		if p == "" {
			continue
		}
		if _, exists := positions[p]; exists {
			positions[p] = -1
		} else {
			positions[p] = i
		}
	}
	out := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		ends := strings.Split(strings.TrimSpace(part), " to ")
		if len(ends) > 2 {
			return nil, false
		}
		a, ok := positions[strings.TrimSpace(ends[0])]
		if !ok || a < 0 {
			return nil, false
		}
		b := a
		if len(ends) == 2 {
			b, ok = positions[strings.TrimSpace(ends[1])]
			if !ok || b < a {
				return nil, false
			}
		}
		for i := a; i <= b; i++ {
			out[i] = true
		}
	}
	return out, true
}

func prepareComposers(e entity, matches []member) []preparedComposer {
	if len(matches) == 0 {
		return nil
	}
	var out []preparedComposer
	seen := map[preparedComposer]bool{}
	add := func(c composerXMLCredit, m member, scope string) {
		if strings.TrimSpace(c.Name) == "" {
			return
		}
		for _, role := range composerRoles(c.Role) {
			value, _ := json.Marshal(core.ComposerCredit{ArtistID: max(0, c.ID), Name: c.Name, NameVariation: c.ANV, Role: role, Scope: scope, Tracks: c.Tracks, TrackPosition: e.Tracks[m.position].Position})
			p := preparedComposer{m.id, string(value)}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for _, c := range e.ExtraArtists {
		if len(composerRoles(c.Role)) == 0 {
			continue
		}
		positions, known := scopedPositions(c.Tracks, e.Tracks)
		for _, m := range matches {
			if known && !positions[m.position] {
				continue
			}
			scope := "release_unknown"
			if known {
				scope = "release_tracks"
			}
			add(c, m, scope)
		}
	}
	for _, m := range matches {
		for _, c := range e.Tracks[m.position].ExtraArtists {
			add(c, m, "track")
		}
	}
	return out
}

// Composers returns source-scoped evidence, including explicitly labeled
// release-level context where track applicability is unknown. Callers must not
// use release_unknown entries to satisfy a strict per-track composer constraint.
func (s *Store) Composers(ctx context.Context, trackID string) ([]core.ComposerCredit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.info.ComposerVersion == "" {
		return nil, nil
	}
	if s.info.ComposerVersion != ComposerVersion {
		return nil, fmt.Errorf("unsupported composer metadata version: %s", s.info.ComposerVersion)
	}
	query := "SELECT source,value FROM composer_credits WHERE track_id=? ORDER BY source,value"
	if s.info.Version == RuntimeVersion {
		query = `SELECT CASE WHEN l.r>0 THEN 'https://www.discogs.com/release/'||l.r ELSE 'https://www.discogs.com/master/'||(-l.r) END source,v.value FROM composer_tracks t JOIN composer_links l ON l.t=t.t JOIN composer_values v ON v.c=l.c WHERE t.id=? ORDER BY source,v.value`
	}
	rows, err := s.db.QueryContext(ctx, query, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.ComposerCredit
	for rows.Next() {
		var source, value string
		if err := rows.Scan(&source, &value); err != nil {
			return nil, err
		}
		var c core.ComposerCredit
		if err := json.Unmarshal([]byte(value), &c); err != nil {
			return nil, err
		}
		c.Source, c.SourceVersion = source, s.info.ComposerVersion+":"+s.info.Date
		out = append(out, c)
	}
	return out, rows.Err()
}
