// Package catalog loads the shipped, read-only Playlist AI catalog: a memory-
// mapped int8 vector file plus a SQLite metadata database. It implements
// ports.Catalog.
package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// File names inside a catalog directory.
const (
	VectorsFile = "vectors.i8"
	DBFile      = "catalog.sqlite"
)

// Catalog is an open catalog directory.
type Catalog struct {
	vec   *vectorStore
	db    *sql.DB
	dim   int
	count int

	ids   []string       // row -> track id
	rowOf map[string]int // track id -> row

	metaStmt            *sql.Stmt
	resolveMax          int
	version             string
	hasAliases          bool
	hasUnicodeSearch    bool
	resolutionMu        sync.RWMutex
	resolutionCache     map[string]core.ReferenceResolution
	representativeCache map[string][]core.WeightedTrack
}

// Open loads the catalog in dir. The directory must contain vectors.i8 and
// catalog.sqlite (as produced by python/convert_pickles.py). The returned
// Catalog must be Closed.
func Open(dir string) (*Catalog, error) {
	vec, err := openVectors(filepath.Join(dir, VectorsFile))
	if err != nil {
		return nil, err
	}

	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, DBFile)) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = vec.close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = vec.close()
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", DBFile, err)
	}

	c := &Catalog{
		vec:                 vec,
		db:                  db,
		dim:                 vec.dim,
		count:               vec.count,
		rowOf:               make(map[string]int, vec.count),
		resolveMax:          200,
		resolutionCache:     make(map[string]core.ReferenceResolution),
		representativeCache: make(map[string][]core.WeightedTrack),
	}

	if err := c.loadIndex(); err != nil {
		_ = c.Close()
		return nil, err
	}

	stmt, err := db.Prepare("SELECT artist, title, preview FROM tracks WHERE id = ?")
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.metaStmt = stmt
	c.loadResolutionMetadata()

	return c, nil
}

func (c *Catalog) loadResolutionMetadata() {
	var format, created, count string
	_ = c.db.QueryRow("SELECT value FROM meta WHERE key = 'format_version'").Scan(&format)
	_ = c.db.QueryRow("SELECT value FROM meta WHERE key = 'created'").Scan(&created)
	_ = c.db.QueryRow("SELECT value FROM meta WHERE key = 'track_count'").Scan(&count)
	c.version = strings.Join([]string{format, count, created}, ":")
	if c.version == "::" {
		c.version = fmt.Sprintf("legacy:%d", c.count)
	}
	var n int
	_ = c.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='artist_aliases'").Scan(&n)
	c.hasAliases = n > 0
	rows, err := c.db.Query("PRAGMA table_info(tracks)")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue any
			if rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk) == nil && name == "unicode_search" {
				c.hasUnicodeSearch = true
			}
		}
	}
}

func (c *Catalog) loadIndex() error {
	rows, err := c.db.Query("SELECT row, id FROM tracks ORDER BY row")
	if err != nil {
		return err
	}
	defer rows.Close()

	c.ids = make([]string, 0, c.count)
	for rows.Next() {
		var row int
		var id string
		if err := rows.Scan(&row, &id); err != nil {
			return err
		}
		if row != len(c.ids) {
			return fmt.Errorf("catalog: row column not contiguous at %d (got %d)", len(c.ids), row)
		}
		c.ids = append(c.ids, id)
		c.rowOf[id] = row
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(c.ids) != c.count {
		return fmt.Errorf("catalog: %d rows in %s but %d vectors", len(c.ids), DBFile, c.count)
	}
	return nil
}

// Close releases the mmap and database handle.
func (c *Catalog) Close() error {
	var errs []error
	if c.metaStmt != nil {
		errs = append(errs, c.metaStmt.Close())
	}
	if c.db != nil {
		errs = append(errs, c.db.Close())
	}
	if c.vec != nil {
		errs = append(errs, c.vec.close())
	}
	return errors.Join(errs...)
}

// --- ports.Catalog -----------------------------------------------------------

func (c *Catalog) Len() int { return c.count }
func (c *Catalog) Dim() int { return c.dim }

func (c *Catalog) ID(row int) string {
	if row < 0 || row >= len(c.ids) {
		return ""
	}
	return c.ids[row]
}

func (c *Catalog) RowOf(id string) (int, bool) {
	r, ok := c.rowOf[id]
	return r, ok
}

func (c *Catalog) Meta(id string) (core.TrackMeta, bool) {
	var artist, title, preview string
	err := c.metaStmt.QueryRow(id).Scan(&artist, &title, &preview)
	if errors.Is(err, sql.ErrNoRows) {
		return core.TrackMeta{}, false
	}
	if err != nil {
		return core.TrackMeta{}, false
	}
	return core.TrackMeta{
		Ref:        core.TrackRef{ID: id, Artist: artist, Title: title},
		PreviewURL: preview,
	}, true
}

func (c *Catalog) VectorsByRow(row int) (ports.Vectors, bool) {
	audio, track := c.vec.at(row)
	if audio == nil {
		return ports.Vectors{}, false
	}
	return ports.Vectors{Audio: audio, Track: track}, true
}

func (c *Catalog) Vectors(id string) (ports.Vectors, bool) {
	row, ok := c.rowOf[id]
	if !ok {
		return ports.Vectors{}, false
	}
	return c.VectorsByRow(row)
}

func (c *Catalog) RawRow(row int) (audio, track []int8, ok bool) {
	return c.vec.rawAt(row)
}

// Resolve runs a token-substring search over the normalized "artist title"
// string: every token must be a substring (Deej-AI's /search semantics).
// Results are row-ordered and capped at max (or an internal ceiling).
// fillerTokens are words that show up in natural-language seed phrases
// ("songs like Radiohead", "a Daft Punk mix") but never in an "Artist -
// Title". They're dropped only as a fallback, when a strict all-token match
// found nothing.
var fillerTokens = map[string]struct{}{
	"song": {}, "songs": {}, "track": {}, "tracks": {}, "tune": {}, "tunes": {},
	"music": {}, "stuff": {}, "vibe": {}, "vibes": {}, "playlist": {}, "mix": {},
	"like": {}, "similar": {}, "radio": {}, "the": {}, "a": {}, "an": {},
	"some": {}, "and": {}, "by": {}, "feat": {}, "ft": {},
}

// Resolve runs a token-substring search over the normalized "artist title"
// (the same normalization as deej-ai.online-app's /search) and returns up to
// max matches in row order.
//
// If a strict match on every token finds nothing, it retries with known filler
// words removed ("songs like Radiohead" -> "Radiohead"). It never drops
// arbitrary trailing words.
func (c *Catalog) Resolve(query string, max int) []core.TrackRef {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}
	if max <= 0 || max > c.resolveMax {
		max = c.resolveMax
	}

	if out := c.resolveTokens(tokens, max); len(out) > 0 {
		return out
	}

	if lean := dropFiller(tokens); len(lean) > 0 && len(lean) < len(tokens) {
		if out := c.resolveTokens(lean, max); len(out) > 0 {
			return out
		}
	}

	return nil
}

func dropFiller(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, drop := fillerTokens[t]; !drop {
			out = append(out, t)
		}
	}
	return out
}

func (c *Catalog) resolveTokens(tokens []string, max int) []core.TrackRef {
	var sb strings.Builder
	sb.WriteString("SELECT id, artist, title FROM tracks WHERE ")
	args := make([]any, 0, len(tokens))
	for i, tok := range tokens {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("search LIKE ?")
		args = append(args, "%"+tok+"%")
	}
	sb.WriteString(" ORDER BY row")

	rows, err := c.db.Query(sb.String(), args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []core.TrackRef
	for rows.Next() {
		var ref core.TrackRef
		if err := rows.Scan(&ref.ID, &ref.Artist, &ref.Title); err != nil {
			break
		}
		out = append(out, ref)
	}
	if len(out) <= 1 {
		return out
	}

	// Stable 4-tier re-rank (row order kept within each tier), then trim to
	// the caller's max:
	//   1. full display or title is exact
	//   2. artist is exactly the query
	//   3. artist contains every token
	//   4. everything else
	joined := strings.Join(tokens, " ")
	tier := func(r core.TrackRef) int {
		a := normalizeSearch(r.Artist)
		if normalizeSearch(r.Display()) == joined || normalizeSearch(r.Title) == joined {
			return 0
		}
		if a == joined {
			return 1
		}
		for _, t := range tokens {
			if !strings.Contains(a, t) {
				return 3
			}
		}
		return 2
	}
	ranked := make([]core.TrackRef, 0, len(out))
	for want := 0; want <= 3; want++ {
		for _, r := range out {
			if tier(r) == want {
				ranked = append(ranked, r)
			}
		}
	}
	if len(ranked) > max {
		ranked = ranked[:max]
	}
	return ranked
}

var _ ports.Catalog = (*Catalog)(nil)
