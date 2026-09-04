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

// Resolve runs a token-substring search over the normalized "artist title"
// string: every token must be a substring (Deej-AI's /search semantics).
// Results are row-ordered and capped at max (or an internal ceiling).
func (c *Catalog) Resolve(query string, max int) []core.TrackRef {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}
	if max <= 0 || max > c.resolveMax {
		max = c.resolveMax
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
	args = append(args, max)

	rows, err := c.db.Query(sb.String(), args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []core.TrackRef
	for rows.Next() {
		var ref core.TrackRef
		if err := rows.Scan(&ref.ID, &ref.Artist, &ref.Title); err != nil {
			return out
		}
		out = append(out, ref)
	}
	return out
}

var _ ports.Catalog = (*Catalog)(nil)
