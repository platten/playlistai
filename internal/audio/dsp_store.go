package audio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// DSPStore borrows its parent Store's connection and lifetime.
type DSPStore struct{ store *Store }

func (s *Store) DSP() *DSPStore { return &DSPStore{store: s} }

func (s *DSPStore) Find(ctx context.Context, catalog, track, key, version string) (core.DSPAnalysis, bool, error) {
	var raw string
	err := s.store.db.QueryRowContext(ctx, `SELECT data FROM dsp_analysis WHERE
catalog=? AND track=? AND track_key=? AND version=? ORDER BY rowid DESC LIMIT 1`, catalog, track, key, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.DSPAnalysis{}, false, nil
	}
	if err != nil {
		return core.DSPAnalysis{}, false, err
	}
	var a core.DSPAnalysis
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return core.DSPAnalysis{}, false, err
	}
	if err := validateDSPAnalysis(a); err != nil {
		return core.DSPAnalysis{}, false, err
	}
	if a.CatalogVersion != catalog || a.TrackID != track || a.TrackKey != key || a.Version != version {
		return core.DSPAnalysis{}, false, fmt.Errorf("audio: cached DSP identity mismatch")
	}
	return a, true, nil
}

func (s *DSPStore) Put(ctx context.Context, a core.DSPAnalysis) error {
	if err := validateDSPAnalysis(a); err != nil {
		return err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO dsp_analysis
(id,catalog,track,track_key,version,data) VALUES(?,?,?,?,?,?)`, a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version, string(raw))
	return err
}

func (s *DSPStore) Usage(ctx context.Context) (core.DSPStorageUsage, error) {
	var usage core.DSPStorageUsage
	err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(CAST(data AS BLOB))),0) FROM dsp_analysis`).Scan(&usage.Records, &usage.Bytes)
	return usage, err
}

func (s *DSPStore) Clear(ctx context.Context) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	_, err := s.store.db.ExecContext(ctx, `DELETE FROM dsp_analysis; VACUUM;`)
	return err
}

func validateDSPAnalysis(a core.DSPAnalysis) error {
	if a.TrackID == "" || a.CatalogVersion == "" || a.TrackKey == "" || a.Version == "" ||
		a.Identity.Status != core.ResolutionResolved || a.Identity.Provider != "deezer" || a.Identity.ProviderID == "" ||
		!representationHash(a.AudioSHA256) || a.SampleRate < 8000 || a.SampleRate > 96000 || a.Channels < 1 || a.Channels > 8 {
		return fmt.Errorf("audio: incomplete or unverified DSP analysis")
	}
	if _, err := time.Parse(time.RFC3339Nano, a.AnalyzedAt); err != nil {
		return fmt.Errorf("audio: invalid DSP analysis time")
	}
	c := a.Coverage
	if !c.Available || c.Source != a.Identity.Provider || !finiteRepresentationTime(c.StartSeconds) ||
		!finiteRepresentationTime(c.EndSeconds) || !finiteRepresentationTime(c.CoveredSeconds) || c.EndSeconds <= c.StartSeconds ||
		c.EndSeconds > MaxPreviewSeconds || math.Abs(c.CoveredSeconds-(c.EndSeconds-c.StartSeconds)) > 1e-9 {
		return fmt.Errorf("audio: invalid DSP observed coverage")
	}
	f := a.Features
	for _, v := range []struct {
		value     core.DSPValue
		low, high float64
	}{
		{f.RMSDBFS, -math.MaxFloat64, 0}, {f.SamplePeakDBFS, -math.MaxFloat64, 0},
		{f.CrestFactorDB, 0, math.MaxFloat64}, {f.RMSWindowSpreadDB, 0, math.MaxFloat64},
		{f.SubbassEnergyRatio, 0, 1}, {f.BassEnergyRatio, 0, 1}, {f.TrebleEnergyRatio, 0, 1},
		{f.SpectralCentroidHz, 20, 12000}, {f.PositiveSpectralFlux, 0, math.MaxFloat64}, {f.OnsetRateHz, 0, float64(a.SampleRate)},
	} {
		switch v.value.State {
		case core.FeatureKnown:
			if v.value.Value == nil || math.IsNaN(*v.value.Value) || math.IsInf(*v.value.Value, 0) || *v.value.Value < v.low || *v.value.Value > v.high {
				return fmt.Errorf("audio: invalid known DSP measurement")
			}
		case core.FeatureUnknown:
			if v.value.Value != nil || v.value.Reason == "" {
				return fmt.Errorf("audio: invalid unknown DSP measurement")
			}
		default:
			return fmt.Errorf("audio: missing DSP measurement state")
		}
	}
	id := a.ID
	a.ID = ""
	if id == "" || id != Fingerprint(a) {
		return fmt.Errorf("audio: DSP fingerprint mismatch")
	}
	return nil
}

var _ ports.DSPStore = (*DSPStore)(nil)
