// Package audio owns local, preview-scoped music analysis. No encoded audio,
// PCM, or intermediate tensors may be written to the analysis store.
package audio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type Store struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "audio-analysis.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS analysis (
 id TEXT PRIMARY KEY, catalog TEXT NOT NULL, track TEXT NOT NULL, track_key TEXT NOT NULL,
 model TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS analysis_lookup ON analysis(catalog, track, track_key, model);
CREATE TABLE IF NOT EXISTS assessment (
 analysis_id TEXT NOT NULL, intent TEXT NOT NULL, policy TEXT NOT NULL, data TEXT NOT NULL,
 PRIMARY KEY(analysis_id, intent, policy));`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func Fingerprint(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (s *Store) Find(ctx context.Context, catalog, track, key string, model core.AudioModelIdentity) (core.AudioAnalysis, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM analysis WHERE catalog=? AND track=? AND track_key=? AND model=? ORDER BY rowid DESC LIMIT 1`, catalog, track, key, Fingerprint(model)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.AudioAnalysis{}, false, nil
	}
	if err != nil {
		return core.AudioAnalysis{}, false, err
	}
	var record core.AudioAnalysis
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return record, false, err
	}
	if err := validateAnalysis(record); err != nil {
		return record, false, err
	}
	id := record.ID
	record.ID = ""
	expected := Fingerprint(record)
	record.ID = id
	if record.Model != model || record.CatalogVersion != catalog || record.TrackID != track || record.TrackKey != key || id != expected {
		return core.AudioAnalysis{}, false, fmt.Errorf("audio: cached analysis identity mismatch")
	}
	return record, true, nil
}

func validateAnalysis(a core.AudioAnalysis) error {
	if a.TrackID == "" || a.CatalogVersion == "" || a.TrackKey == "" || a.Model.Model == "" || a.Model.Revision == "" || a.Model.Preprocessing == "" || a.Model.Runtime == "" || a.Identity.Status != core.ResolutionResolved || a.Identity.Provider != "deezer" || a.Identity.ProviderID == "" || len(a.Segments) == 0 || len(a.Segments) > 6 {
		return fmt.Errorf("audio: incomplete or unverified analysis")
	}
	if raw, err := hex.DecodeString(a.AudioSHA256); err != nil || len(raw) != 32 {
		return fmt.Errorf("audio: invalid audio hash")
	}
	for _, segment := range a.Segments {
		if segment.StartSeconds < 0 || math.IsNaN(segment.StartSeconds) || math.IsInf(segment.StartSeconds, 0) || segment.EndSeconds <= segment.StartSeconds || math.IsNaN(segment.EndSeconds) || math.IsInf(segment.EndSeconds, 0) || !validVector(segment.Embedding, a.Model.Dimension) {
			return fmt.Errorf("audio: invalid segment")
		}
	}
	if a.Sampling != nil {
		if err := validateSampling(a); err != nil {
			return err
		}
	}
	return nil
}

func validVector(v []float32, dimension int) bool {
	if dimension < 1 || dimension > 8192 || len(v) != dimension {
		return false
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
		norm += float64(x) * float64(x)
	}
	return norm > 0.9 && norm < 1.1
}

func (s *Store) Put(ctx context.Context, a core.AudioAnalysis) error {
	if err := validateAnalysis(a); err != nil {
		return err
	}
	id := a.ID
	a.ID = ""
	if id == "" || id != Fingerprint(a) {
		return fmt.Errorf("audio: analysis identity mismatch")
	}
	a.ID = id
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO analysis(id,catalog,track,track_key,model,data) VALUES(?,?,?,?,?,?)`, a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), string(raw))
	return err
}

func (s *Store) PutAssessment(ctx context.Context, a core.AudioAssessment) error {
	if a.AnalysisID == "" || a.IntentFingerprint == "" || a.PolicyVersion == "" {
		return fmt.Errorf("audio: incomplete assessment")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO assessment(analysis_id,intent,policy,data) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM analysis WHERE id=?)`, a.AnalysisID, a.IntentFingerprint, a.PolicyVersion, string(raw), a.AnalysisID)
	return err
}

func (s *Store) Usage(ctx context.Context) (core.AnalysisStorageUsage, error) {
	var usage core.AnalysisStorageUsage
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM analysis), (SELECT COUNT(*) FROM assessment)`).Scan(&usage.Records, &usage.Assessments)
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm", s.path + "-journal"} {
		if stat, e := os.Stat(path); e == nil {
			usage.Bytes += stat.Size()
		}
	}
	return usage, err
}

// Clear is explicit retention control; upgrades never delete incompatible rows.
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM assessment; DELETE FROM analysis; VACUUM;`)
	return err
}

var _ ports.AnalysisStore = (*Store)(nil)
