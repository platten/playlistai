package taste

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const FileName = "taste.sqlite"

// Exposure retention. Generation writes one exposure row per recommended
// track, so exposures are the only event type whose volume scales with usage.
// They decay on exposureHalfLife, so anything older than ExposureRetention
// contributes under 2^-5 of a fresh hit and is not worth reading or storing.
// MaxExposureEvents additionally bounds a single long session, where rows can
// accumulate faster than the time horizon retires them; the newest events are
// kept because the projection is recency-weighted.
//
// Explicit feedback (likes, dislikes, acceptance) is user-authored, arrives one
// event per interaction, and is never pruned or windowed here.
const (
	ExposureRetention = 5 * exposureHalfLife
	// Matches the maxRecentExposures map bound the events feed: reading further
	// back cannot add entries beyond that cap, only marginally re-weight ones
	// already saturated by 1 - exp(-weight).
	MaxExposureEvents = maxRecentExposures
)

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
		// Serves both the windowed newest-first exposure read and the retention
		// sweep, so neither scans explicit feedback.
		`CREATE INDEX IF NOT EXISTS idx_feedback_type_time ON feedback_events(type, occurred_at, id)`,
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
	s := &Store{db: db, now: time.Now}
	// One retention sweep per launch keeps the file from growing without bound.
	// Deliberately best-effort: stale rows cost space, not correctness, and
	// ListFeedback windows them out either way, so a failure must not stop the
	// app from starting.
	_ = s.PruneExposures(context.Background(), s.now().Add(-ExposureRetention))
	return s, nil
}

// PruneExposures deletes exposure rows older than before. Explicit feedback is
// never removed — only generation-emitted exposure telemetry.
func (s *Store) PruneExposures(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM feedback_events WHERE type = ? AND occurred_at < ?`,
		core.FeedbackExposure, before.UnixNano())
	if err != nil {
		return fmt.Errorf("taste: prune exposures: %w", err)
	}
	return nil
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

const selectFeedbackColumns = `SELECT id, version, occurred_at, type, scope, track_id,
		request_id, session_id, context_json, versions_json FROM feedback_events`

// scopeClause matches events belonging to this profile's context. An empty
// RequestID/SessionID means "no context", so the corresponding column is not
// compared — otherwise it would match every row that recorded no request or no
// session, pulling unrelated evidence into a scoped profile.
const scopeClause = `(
			(? = '' AND ? = '')
			OR scope = 'durable'
			OR (? <> '' AND request_id = ?)
			OR (? <> '' AND session_id = ?)
		)`

// ListFeedback returns explicit feedback in full, plus exposures bounded by
// ExposureRetention and MaxExposureEvents. Exposure volume scales with every
// generation, so an unbounded read would make each generation cost more than
// the last; the projection is recency-weighted, so the newest rows are the
// ones that matter.
//
// Ties on occurred_at break by rowid, which is insertion order in this
// append-only table. Breaking them by id instead returns a different
// permutation on every read, because ids are random hex. Timestamps tie
// whenever a batch is written faster than the platform clock advances — routine
// on Windows, and possible anywhere.
func (s *Store) ListFeedback(ctx context.Context, query ports.FeedbackQuery) ([]core.FeedbackEvent, error) {
	events, err := s.queryFeedback(ctx,
		selectFeedbackColumns+` WHERE type <> ? AND `+scopeClause+` ORDER BY occurred_at, rowid`,
		core.FeedbackExposure,
		query.RequestID, query.SessionID,
		query.RequestID, query.RequestID,
		query.SessionID, query.SessionID)
	if err != nil {
		return nil, err
	}
	exposures, err := s.queryFeedback(ctx,
		selectFeedbackColumns+` WHERE type = ? AND occurred_at >= ? AND (? OR `+scopeClause+`)
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		core.FeedbackExposure, s.now().Add(-ExposureRetention).UnixNano(), query.IncludeExposures,
		query.RequestID, query.SessionID,
		query.RequestID, query.RequestID,
		query.SessionID, query.SessionID,
		MaxExposureEvents)
	if err != nil {
		return nil, err
	}
	return append(events, exposures...), nil
}

func (s *Store) queryFeedback(ctx context.Context, statement string, args ...any) ([]core.FeedbackEvent, error) {
	rows, err := s.db.QueryContext(ctx, statement, args...)
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

func (s *Store) ProfileByID(ctx context.Context, snapshotID string) (core.TasteProfile, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT profile_json FROM taste_profiles WHERE snapshot_id = ?`, snapshotID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.TasteProfile{}, false, nil
	}
	if err != nil {
		return core.TasteProfile{}, false, fmt.Errorf("taste: snapshot: %w", err)
	}
	var profile core.TasteProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return core.TasteProfile{}, false, fmt.Errorf("taste: decode snapshot: %w", err)
	}
	return profile, true, nil
}

func (s *Store) LatestProfile(ctx context.Context, catalogVersion, requestID, sessionID string) (core.TasteProfile, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT profile_json FROM taste_profiles
		WHERE catalog_version = ? AND request_id = ? AND session_id = ?
		ORDER BY saved_at DESC LIMIT 1`, catalogVersion, requestID, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
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
