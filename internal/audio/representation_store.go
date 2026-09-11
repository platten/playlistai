package audio

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// RepresentationStore shares its parent's database and lifetime. Close the
// parent Store only after all analysis consumers have stopped.
type RepresentationStore struct{ store *Store }

func (s *Store) Representations() *RepresentationStore { return &RepresentationStore{store: s} }

func (s *RepresentationStore) Find(ctx context.Context, catalog, track, key string, model core.AudioRepresentationIdentity) (core.AudioRepresentation, bool, error) {
	var raw string
	err := s.store.db.QueryRowContext(ctx, `SELECT data FROM audio_representation
WHERE catalog=? AND track=? AND track_key=? AND model=? ORDER BY rowid DESC LIMIT 1`,
		catalog, track, key, Fingerprint(model)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.AudioRepresentation{}, false, nil
	}
	if err != nil {
		return core.AudioRepresentation{}, false, err
	}
	var a core.AudioRepresentation
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return core.AudioRepresentation{}, false, err
	}
	if err := validateRepresentation(a); err != nil {
		return core.AudioRepresentation{}, false, err
	}
	if a.Model != model || a.CatalogVersion != catalog || a.TrackID != track || a.TrackKey != key {
		return core.AudioRepresentation{}, false, fmt.Errorf("audio: cached representation identity mismatch")
	}
	return a, true, nil
}

func (s *RepresentationStore) Put(ctx context.Context, a core.AudioRepresentation) error {
	if err := validateRepresentation(a); err != nil {
		return err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO audio_representation
(id,catalog,track,track_key,model,data) VALUES(?,?,?,?,?,?)`,
		a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), string(raw))
	return err
}

func (s *RepresentationStore) Usage(ctx context.Context) (core.AudioRepresentationStorageUsage, error) {
	var usage core.AudioRepresentationStorageUsage
	err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(CAST(data AS BLOB))),0)
FROM audio_representation`).Scan(&usage.Records, &usage.Bytes)
	return usage, err
}

// Clear preserves CLAP analyses/assessments and does not delete the shared file.
func (s *RepresentationStore) Clear(ctx context.Context) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	_, err := s.store.db.ExecContext(ctx, `DELETE FROM audio_representation; VACUUM;`)
	return err
}

func validateRepresentation(a core.AudioRepresentation) error {
	m := a.Model
	if a.TrackID == "" || a.CatalogVersion == "" || a.TrackKey == "" ||
		m.Model == "" || m.Revision == "" || m.Preprocessing == "" || m.Runtime == "" || m.Pooling == "" ||
		!representationHash(m.WeightsSHA256) || !representationHash(a.AudioSHA256) ||
		a.Identity.Status != core.ResolutionResolved || a.Identity.Provider != "deezer" || a.Identity.ProviderID == "" {
		return fmt.Errorf("audio: incomplete or unverified representation")
	}
	if _, err := time.Parse(time.RFC3339Nano, a.AnalyzedAt); err != nil {
		return fmt.Errorf("audio: invalid representation analysis time")
	}
	if !validVector(a.Pooled, m.Dimension) || len(a.Segments) == 0 || len(a.Segments) > 12 {
		return fmt.Errorf("audio: invalid representation vectors")
	}
	c := a.Coverage
	if !c.Available || c.Source != a.Identity.Provider || !finiteRepresentationTime(c.StartSeconds) ||
		!finiteRepresentationTime(c.EndSeconds) || !finiteRepresentationTime(c.CoveredSeconds) ||
		c.EndSeconds <= c.StartSeconds || c.EndSeconds > MaxPreviewSeconds || c.CoveredSeconds <= 0 {
		return fmt.Errorf("audio: invalid representation coverage")
	}
	var observed float64
	previous := c.StartSeconds
	for _, segment := range a.Segments {
		if !finiteRepresentationTime(segment.StartSeconds) || !finiteRepresentationTime(segment.EndSeconds) ||
			segment.StartSeconds < previous || segment.EndSeconds <= segment.StartSeconds ||
			segment.EndSeconds > c.EndSeconds || !validVector(segment.Vector, m.Dimension) {
			return fmt.Errorf("audio: invalid representation segment")
		}
		observed += segment.EndSeconds - segment.StartSeconds
		previous = segment.EndSeconds
	}
	if a.Segments[0].StartSeconds != c.StartSeconds || previous != c.EndSeconds || math.Abs(observed-c.CoveredSeconds) > 1e-6 {
		return fmt.Errorf("audio: representation coverage differs from observed segments")
	}
	id := a.ID
	a.ID = ""
	if id == "" || id != Fingerprint(a) {
		return fmt.Errorf("audio: representation fingerprint mismatch")
	}
	return nil
}

func representationHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func finiteRepresentationTime(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

var _ ports.AudioRepresentationStore = (*RepresentationStore)(nil)
