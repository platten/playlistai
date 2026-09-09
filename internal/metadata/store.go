// Package metadata provides an optional, catalog-matched Discogs bulk index.
// Release/master tags are retrieval hints, not recording-level musical evidence.
package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
)

const Version = "discogs-catalog/v1"

type Info struct {
	Version         string            `json:"version"`
	Date            string            `json:"date"`
	Catalog         string            `json:"catalog"`
	License         string            `json:"license"`
	Inputs          map[string]string `json:"inputs"`
	Scanned         int64             `json:"scanned"`
	Entities        int64             `json:"entities"`
	Tracks          int64             `json:"tracks"`
	ComposerVersion string            `json:"composerVersion,omitempty"`
	ComposerCredits int64             `json:"composerCredits,omitempty"`
}

type Match struct {
	TrackID  string
	Source   string
	ArtistID int64
	Artists  []string
}

type Store struct {
	db   *sql.DB
	info Info
}

func Open(path string) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", readOnlyURI(path))
	if err != nil {
		return nil, err
	}
	var raw string
	if err = db.QueryRow("SELECT value FROM info WHERE key='manifest'").Scan(&raw); err != nil {
		_ = db.Close()
		return nil, err
	}
	var info Info
	if err = json.Unmarshal([]byte(raw), &info); err != nil || (info.Version != Version && info.Version != RuntimeVersion) || info.Catalog == "" || info.License != "CC0-1.0" || info.Tracks <= 0 {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported or incomplete local metadata dataset")
	}
	if _, err = time.Parse("20060102", info.Date); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("invalid local metadata snapshot date")
	}
	return &Store{db: db, info: info}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Info() Info {
	info := s.info
	info.Inputs = maps.Clone(info.Inputs)
	return info
}
func (s *Store) Compatible(catalog string) bool { return s != nil && s.info.Catalog == catalog }

// Genre performs an indexed exact concept lookup, never title/artist guessing.
func (s *Store) Genre(ctx context.Context, genre string, limit int) ([]Match, error) {
	return s.SampleGenre(ctx, genre, limit, 0)
}

// SampleGenre seeks into a stable hash index and wraps once. A seeded pivot
// reaches the entire category without random sorting or an artist-ID prefix.
// This is a bounded retrieval window, not an unbiased statistical sample.
func (s *Store) SampleGenre(ctx context.Context, genre string, limit int, pivot uint32) ([]Match, error) {
	limit = min(10000, max(1, limit))
	out, err := s.genreWindow(ctx, genre, limit, pivot, false)
	if err != nil || len(out) == limit || pivot == 0 {
		return out, err
	}
	wrapped, err := s.genreWindow(ctx, genre, limit-len(out), pivot, true)
	return append(out, wrapped...), err
}

func (s *Store) genreWindow(ctx context.Context, genre string, limit int, pivot uint32, wrap bool) ([]Match, error) {
	comparison := ">="
	if wrap {
		comparison = "<"
	}
	query := `SELECT track_id,source,artist_id,credits FROM genre_tracks WHERE tag=? AND sample_key ` + comparison + ` ? ORDER BY sample_key,artist_id,track_id LIMIT ?`
	position := uint64(pivot)
	if s.info.Version == RuntimeVersion {
		position = uint64(pivot)*uint64(s.info.Tracks)>>32 + 1
		query = `SELECT t.id,CASE WHEN c.r>0 THEN 'https://www.discogs.com/release/'||c.r ELSE 'https://www.discogs.com/master/'||(-c.r) END,c.a,k.names FROM genre_keys g JOIN candidates c ON c.g=g.g JOIN recordings t ON t.t=c.t JOIN credit_sets k ON k.c=c.c WHERE g.key=? AND c.t ` + comparison + ` ? ORDER BY c.t,c.a LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, query, core.NormalizeIdentityPart(genre), position, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		var credits string
		if err := rows.Scan(&m.TrackID, &m.Source, &m.ArtistID, &credits); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(credits), &m.Artists); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) HasGenre(ctx context.Context, genre string) bool {
	var one int
	if s.info.Version == RuntimeVersion {
		return s.db.QueryRowContext(ctx, "SELECT 1 FROM genre_keys g JOIN candidates c ON c.g=g.g WHERE g.key=? LIMIT 1", core.NormalizeIdentityPart(genre)).Scan(&one) == nil
	}
	return s.db.QueryRowContext(ctx, "SELECT 1 FROM genre_tracks WHERE tag=? LIMIT 1", core.NormalizeIdentityPart(genre)).Scan(&one) == nil
}
func (s *Store) Genres(ctx context.Context) (core.GenreGraph, error) {
	query := "SELECT name FROM tags ORDER BY name"
	if s.info.Version == RuntimeVersion {
		query = "SELECT name FROM genre_keys ORDER BY name"
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return core.GenreGraph{}, err
	}
	defer rows.Close()
	g := core.GenreGraph{Version: s.info.Version + ":" + s.info.Date}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return g, err
		}
		g.Nodes = append(g.Nodes, core.GenreNode{ID: "discogs-dump:" + core.NormalizeIdentityPart(name), Name: name})
	}
	return g, rows.Err()
}

// Reference uses explicit album identity. Incomplete catalog coverage is not
// represented as a complete required album; callers may use tracks as anchors.
func (s *Store) Reference(ctx context.Context, ref core.IntentReference) ([]Match, error) {
	if s.info.Version == RuntimeVersion {
		return nil, nil
	} // intentionally omitted; callers retain API fallback
	artist, title := "", ref.Query
	if ref.Kind == core.ReferenceAlbum {
		if a, t, ok := core.QualifiedReferenceParts(ref.Query); ok {
			artist, title = a, t
		}
	}
	query := `SELECT DISTINCT t.track_id,e.source,t.artist_id FROM entities e JOIN entity_tracks t ON t.entity=e.source WHERE e.title_key=?`
	args := []any{core.NormalizeIdentityPart(title)}
	if ref.Kind == core.ReferenceArtist {
		query = `SELECT DISTINCT t.track_id,e.source,t.artist_id FROM entities e JOIN entity_tracks t ON t.entity=e.source WHERE t.artist_key=?`
		args = []any{core.NormalizeIdentityPart(ref.Query)}
	} else if ref.Kind != core.ReferenceAlbum {
		return nil, nil
	}
	if artist != "" {
		query += " AND t.artist_key=?"
		args = append(args, core.NormalizeIdentityPart(artist))
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY e.source,t.position LIMIT 101", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.TrackID, &m.Source, &m.ArtistID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func artistName(name string) string {
	// Discogs appends numeric disambiguators; preserve IDs separately. The
	// artist/title join is provisional and never establishes a canonical ID.
	if i := strings.LastIndex(name, " ("); i >= 0 && strings.HasSuffix(name, ")") {
		digits := name[i+2 : len(name)-1]
		if digits != "" && strings.Trim(digits, "0123456789") == "" {
			return name[:i]
		}
	}
	return name
}
