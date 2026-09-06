// Package history persists generated playlists to a small SQLite database so the
// Generate screen can offer past prompts as a starting point. It stores the
// user's prompt, a short human title, and opaque JSON blobs (intent, request,
// tracks, and complete generation result) that the bridge layer owns the shape
// of.
package history

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// FileName is the database file, created under the app data dir.
const FileName = "history.sqlite"

// Store is a handle to the playlist-history database.
type Store struct {
	db *sql.DB
}

// Record is one saved playlist. The *JSON fields are stored verbatim; callers
// marshal/unmarshal them.
type Record struct {
	ID          string
	CreatedAt   time.Time
	Name        string
	Prompt      string
	Notes       string
	Mode        string
	TrackCount  int
	IntentJSON  []byte
	RequestJSON []byte
	TracksJSON  []byte
	ResultJSON  []byte
}

// Open opens (creating if needed) the history database under dataDir.
func Open(dataDir string) (*Store, error) {
	dsn := "file:" + filepath.Join(dataDir, FileName) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS playlists (
		id           TEXT PRIMARY KEY,
		created_at   INTEGER NOT NULL,
		name         TEXT NOT NULL,
		prompt       TEXT NOT NULL,
		notes        TEXT NOT NULL DEFAULT '',
		mode         TEXT NOT NULL DEFAULT '',
		track_count  INTEGER NOT NULL DEFAULT 0,
		intent_json  TEXT NOT NULL DEFAULT '{}',
		request_json TEXT NOT NULL DEFAULT '{}',
		tracks_json  TEXT NOT NULL DEFAULT '[]',
		result_json  TEXT NOT NULL DEFAULT '{}'
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureResultColumn(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_playlists_created_at ON playlists(created_at DESC)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Save inserts rec, filling ID and CreatedAt when they are unset, and returns
// the stored record.
func (s *Store) Save(ctx context.Context, rec Record) (Record, error) {
	if rec.ID == "" {
		rec.ID = newID()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO playlists
		   (id, created_at, name, prompt, notes, mode, track_count, intent_json, request_json, tracks_json, result_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.CreatedAt.Unix(), rec.Name, rec.Prompt, rec.Notes, rec.Mode, rec.TrackCount,
		blobOr(rec.IntentJSON, "{}"), blobOr(rec.RequestJSON, "{}"), blobOr(rec.TracksJSON, "[]"), blobOr(rec.ResultJSON, "{}"),
	)
	if err != nil {
		return Record{}, fmt.Errorf("history: save: %w", err)
	}
	return rec, nil
}

// List returns saved playlists newest-first, at most limit (<=0 means 50).
func (s *Store) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, name, prompt, notes, mode, track_count, intent_json, request_json, tracks_json, result_json
		   FROM playlists ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("history: list: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		rec, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Get returns the record with id, or ok=false if there is none.
func (s *Store) Get(ctx context.Context, id string) (Record, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, name, prompt, notes, mode, track_count, intent_json, request_json, tracks_json, result_json
		   FROM playlists WHERE id = ?`, id)
	rec, err := scan(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Record{}, false, nil
	case err != nil:
		return Record{}, false, err
	}
	return rec, true, nil
}

// Delete removes the record with id. Missing ids are not an error.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner) (Record, error) {
	var (
		rec         Record
		createdUnix int64
		intentJSON  string
		requestJSON string
		tracksJSON  string
		resultJSON  string
	)
	if err := r.Scan(&rec.ID, &createdUnix, &rec.Name, &rec.Prompt, &rec.Notes,
		&rec.Mode, &rec.TrackCount, &intentJSON, &requestJSON, &tracksJSON, &resultJSON); err != nil {
		return Record{}, err
	}
	rec.CreatedAt = time.Unix(createdUnix, 0)
	rec.IntentJSON = []byte(intentJSON)
	rec.RequestJSON = []byte(requestJSON)
	rec.TracksJSON = []byte(tracksJSON)
	rec.ResultJSON = []byte(resultJSON)
	return rec, nil
}

func ensureResultColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(playlists)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "result_json" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE playlists ADD COLUMN result_json TEXT NOT NULL DEFAULT '{}'`)
	return err
}

func blobOr(b []byte, fallback string) string {
	if len(b) == 0 {
		return fallback
	}
	return string(b)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
