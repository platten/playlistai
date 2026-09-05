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

	metaStmt   *sql.Stmt
	resolveMax int
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
		vec:        vec,
		db:         db,
		dim:        vec.dim,
		count:      vec.count,
		rowOf:      make(map[string]int, vec.count),
		resolveMax: 200,
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

	return c, nil
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
// If a strict match on every token finds nothing, it retries with filler
// words removed ("songs like Radiohead" -> "Radiohead"), then by dropping
// trailing tokens one at a time — so a seed phrase with a real artist name in
// it still resolves even when the LLM or the rules parser tacked on extra
// words. A non-empty strict result is never overridden by the fallback.
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
		tokens = lean
	}

	for n := len(tokens) - 1; n >= 1; n-- {
		if out := c.resolveTokens(tokens[:n], max); len(out) > 0 {
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

// rankWindow is how many row-ordered matches resolveTokens pulls before
// re-ranking, so that a small max (seed resolution passes max=1) can still
// prefer a track whose *artist* matches over an incidental title hit —
// "justice" should seed from Justice, not "…King's Justice" by Ramin Djawadi.
const rankWindow = 60

func (c *Catalog) resolveTokens(tokens []string, max int) []core.TrackRef {
	limit := max
	if limit < rankWindow {
		limit = rankWindow
	}

	var sb strings.Builder
	sb.WriteString("SELECT id, artist, title FROM tracks WHERE ")
	args := make([]any, 0, len(tokens)+1)
	for i, tok := range tokens {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("search LIKE ?")
		args = append(args, "%"+tok+"%")
	}
	sb.WriteString(" ORDER BY row LIMIT ?")
	args = append(args, limit)

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

	// Stable 3-tier re-rank (row order kept within each tier), then trim to
	// the caller's max:
	//   1. artist is exactly the query    ("justice" -> Justice, not Justice Toch)
	//   2. artist contains every token    (an incidental-title hit loses to this)
	//   3. everything else                (title-only matches)
	joined := strings.Join(tokens, " ")
	tier := func(r core.TrackRef) int {
		a := normalizeSearch(r.Artist)
		if a == joined {
			return 0
		}
		for _, t := range tokens {
			if !strings.Contains(a, t) {
				return 2
			}
		}
		return 1
	}
	ranked := make([]core.TrackRef, 0, len(out))
	for want := 0; want <= 2; want++ {
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
