package taste

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const FileName = "taste.sqlite"

// Store is the local SQLite implementation of both feedback and profile ports.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(dataDir string) (*Store, error) {
	dsn := "file:" + filepath.Join(dataDir, FileName) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS feedback_events (
			id TEXT PRIMARY KEY,
			version INTEGER NOT NULL,
			occurred_at INTEGER NOT NULL,
			type TEXT NOT NULL,
			scope TEXT NOT NULL,
			track_id TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			context_json TEXT NOT NULL DEFAULT '{}',
			versions_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feedback_time ON feedback_events(occurred_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_feedback_request ON feedback_events(request_id, session_id)`,
		`CREATE TABLE IF NOT EXISTS taste_profiles (
			snapshot_id TEXT NOT NULL,
			saved_at INTEGER NOT NULL,
			catalog_version TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			profile_json TEXT NOT NULL,
			PRIMARY KEY(snapshot_id, request_id, session_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_lookup ON taste_profiles(catalog_version, request_id, session_id, saved_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("taste: initialize store: %w", err)
		}
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) RecordFeedback(ctx context.Context, event core.FeedbackEvent) (core.FeedbackEvent, error) {
	event = s.prepare(event)
	if err := event.Validate(); err != nil {
		return core.FeedbackEvent{}, err
	}
	if err := insertEvent(ctx, s.db, event); err != nil {
		return core.FeedbackEvent{}, err
	}
	return event, nil
}

func (s *Store) RecordFeedbackBatch(ctx context.Context, events []core.FeedbackEvent) error {
	if len(events) == 0 {
		return nil
	}
	prepared := make([]core.FeedbackEvent, len(events))
	for index, event := range events {
		prepared[index] = s.prepare(event)
		if err := prepared[index].Validate(); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("taste: begin feedback batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, event := range prepared {
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("taste: commit feedback batch: %w", err)
	}
	return nil
}

type eventExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertEvent(ctx context.Context, execer eventExecer, event core.FeedbackEvent) error {
	contextJSON, err := json.Marshal(event.Context)
	if err != nil {
		return err
	}
	versionsJSON, err := json.Marshal(event.Versions)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, `INSERT INTO feedback_events
		(id, version, occurred_at, type, scope, track_id, request_id, session_id, context_json, versions_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Version, event.OccurredAt.UnixNano(), event.Type, event.Scope,
		event.TrackID, event.RequestID, event.SessionID, contextJSON, versionsJSON)
	if err != nil {
		return fmt.Errorf("taste: record feedback: %w", err)
	}
	return nil
}

func (s *Store) prepare(event core.FeedbackEvent) core.FeedbackEvent {
	if event.ID == "" {
		event.ID = newEventID()
	}
	if event.Version == 0 {
		event.Version = core.FeedbackEventVersion
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	return event
}

func (s *Store) ListFeedback(ctx context.Context, query ports.FeedbackQuery) ([]core.FeedbackEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, occurred_at, type, scope, track_id,
		request_id, session_id, context_json, versions_json
		FROM feedback_events
		WHERE (? = '' AND ? = '') OR scope = 'durable' OR request_id = ? OR session_id = ?
		ORDER BY occurred_at, id`, query.RequestID, query.SessionID, query.RequestID, query.SessionID)
	if err != nil {
		return nil, fmt.Errorf("taste: list feedback: %w", err)
	}
	defer rows.Close()
	events := []core.FeedbackEvent{}
	for rows.Next() {
		var event core.FeedbackEvent
		var occurred int64
		var contextJSON, versionsJSON []byte
		if err := rows.Scan(&event.ID, &event.Version, &occurred, &event.Type, &event.Scope,
			&event.TrackID, &event.RequestID, &event.SessionID, &contextJSON, &versionsJSON); err != nil {
			return nil, err
		}
		event.OccurredAt = time.Unix(0, occurred).UTC()
		if err := json.Unmarshal(contextJSON, &event.Context); err != nil {
			return nil, fmt.Errorf("taste: decode feedback context: %w", err)
		}
		if err := json.Unmarshal(versionsJSON, &event.Versions); err != nil {
			return nil, fmt.Errorf("taste: decode feedback versions: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ClearFeedback(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM feedback_events`)
	return err
}

func (s *Store) SaveProfile(ctx context.Context, profile core.TasteProfile) error {
	if profile.Version != ProfileContractVersion || profile.AlgorithmVersion != ProfileAlgorithmVersion || profile.SnapshotID == "" {
		return fmt.Errorf("taste: invalid profile identity")
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("taste: encode profile: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO taste_profiles
		(snapshot_id, saved_at, catalog_version, request_id, session_id, profile_json)
		VALUES (?, ?, ?, ?, ?, ?)`, profile.SnapshotID, s.now().UnixNano(), profile.CatalogVersion,
		profile.RequestID, profile.SessionID, raw)
	if err != nil {
		return fmt.Errorf("taste: save profile: %w", err)
	}
	return nil
}

func (s *Store) LatestProfile(ctx context.Context, catalogVersion, requestID, sessionID string) (core.TasteProfile, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT profile_json FROM taste_profiles
		WHERE catalog_version = ? AND request_id = ? AND session_id = ?
		ORDER BY saved_at DESC LIMIT 1`, catalogVersion, requestID, sessionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return core.TasteProfile{}, false, nil
	}
	if err != nil {
		return core.TasteProfile{}, false, fmt.Errorf("taste: latest profile: %w", err)
	}
	var profile core.TasteProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return core.TasteProfile{}, false, fmt.Errorf("taste: decode profile: %w", err)
	}
	return profile, true, nil
}

func (s *Store) ClearProfiles(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM taste_profiles`)
	return err
}

func newEventID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("event-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

var _ ports.FeedbackStore = (*Store)(nil)
var _ ports.ProfileStore = (*Store)(nil)
