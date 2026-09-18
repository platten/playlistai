package libraryindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/librarysearch"
	"github.com/platten/playlistai/internal/sqliteuri"
)

const (
	assignmentStoreName = "assignments.sqlite"
	streamBatchRows     = 4096
)

type frozenStore struct {
	db *sql.DB
}

func openFrozenStore(path string) (*frozenStore, error) {
	dsn, err := sqliteuri.ReadOnly(path, true)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &frozenStore{db: db}, nil
}

func (s *frozenStore) Close() error { return s.db.Close() }

func (s *frozenStore) summary(ctx context.Context) (librarypack.Coverage, librarypack.VectorSpace, int, error) {
	var coverage librarypack.Coverage
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='metadata' AND j.state='completed' WHERE f.status='present'),
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='metadata' AND j.state='completed' WHERE f.status='present'),
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='completed' JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=j.semantic_key WHERE f.status='present' AND length(v.vector)>0),
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='completed' JOIN dsp_results d ON d.file_id=f.id AND d.source_revision=f.source_revision AND d.contract=j.semantic_key WHERE f.status='present' AND length(d.data)>0),
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='failed' AND j.error_code<>'unsupported' WHERE f.status='present'),
		(SELECT COUNT(*) FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='failed' AND j.error_code='unsupported' WHERE f.status='present')`).Scan(
		&coverage.Tracks, &coverage.Metadata, &coverage.MERT, &coverage.DSP, &coverage.Failed, &coverage.Unsupported)
	if err != nil {
		return coverage, librarypack.VectorSpace{}, 0, err
	}
	if coverage.MERT == 0 {
		return coverage, librarypack.VectorSpace{}, 0, nil
	}
	var metadataRaw, mertRaw, vectorRaw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT m.data,v.data,v.vector
		FROM files f
		JOIN jobs jm ON jm.file_id=f.id AND jm.source_revision=f.source_revision AND jm.kind='metadata' AND jm.state='completed'
		JOIN track_metadata m ON m.file_id=f.id AND m.source_revision=f.source_revision AND m.contract=jm.semantic_key
		JOIN jobs ja ON ja.file_id=f.id AND ja.source_revision=f.source_revision AND ja.kind='audio' AND ja.state='completed'
		JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=ja.semantic_key
		WHERE f.status='present' AND length(v.vector)>0 ORDER BY f.id LIMIT 1`).Scan(&metadataRaw, &mertRaw, &vectorRaw); err != nil {
		return coverage, librarypack.VectorSpace{}, 0, err
	}
	vector, err := decodeFloat32Vector(vectorRaw)
	if err != nil {
		return coverage, librarypack.VectorSpace{}, 0, err
	}
	defer clear(vector)
	var metadata MetadataRecord
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return coverage, librarypack.VectorSpace{}, 0, err
	}
	space := vectorSpaceFromFields(metadata, mertRaw, len(vector))
	return coverage, space, len(vector), nil
}

type frozenMetadataSource struct{ rows *sql.Rows }

func (s *frozenStore) metadataSource(ctx context.Context) (*frozenMetadataSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,m.data
		FROM files f
		JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='metadata' AND j.state='completed'
		JOIN track_metadata m ON m.file_id=f.id AND m.source_revision=f.source_revision AND m.contract=j.semantic_key
		WHERE f.status='present' ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return &frozenMetadataSource{rows: rows}, nil
}

func (s *frozenMetadataSource) Next(ctx context.Context) (librarylearn.MetadataTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarylearn.MetadataTrack{}, false, err
	}
	if !s.rows.Next() {
		return librarylearn.MetadataTrack{}, false, s.rows.Err()
	}
	var id string
	var raw []byte
	if err := s.rows.Scan(&id, &raw); err != nil {
		return librarylearn.MetadataTrack{}, false, err
	}
	var record MetadataRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return librarylearn.MetadataTrack{}, false, fmt.Errorf("track %s metadata: %w", id, err)
	}
	return metadataLearningFields(id, record), true, nil
}

func (s *frozenMetadataSource) Close() error { return s.rows.Close() }

type frozenVectorSource struct{ rows *sql.Rows }

func (s *frozenStore) vectorSource(ctx context.Context) (*frozenVectorSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,v.vector
		FROM files f
		JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='completed'
		JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=j.semantic_key
		WHERE f.status='present' AND length(v.vector)>0 ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return &frozenVectorSource{rows: rows}, nil
}

func (s *frozenVectorSource) Next(ctx context.Context) (librarylearn.DenseVector, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarylearn.DenseVector{}, false, err
	}
	if !s.rows.Next() {
		return librarylearn.DenseVector{}, false, s.rows.Err()
	}
	var id string
	var raw []byte
	if err := s.rows.Scan(&id, &raw); err != nil {
		return librarylearn.DenseVector{}, false, err
	}
	vector, err := decodeFloat32Vector(raw)
	if err != nil {
		return librarylearn.DenseVector{}, false, fmt.Errorf("track %s: %w", id, err)
	}
	return librarylearn.DenseVector{ID: id, Values: vector}, true, nil
}

func (s *frozenVectorSource) Close() error { return s.rows.Close() }

type frozenDSPSource struct{ rows *sql.Rows }

func (s *frozenStore) dspSource(ctx context.Context) (*frozenDSPSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,d.data
		FROM files f
		JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='completed'
		JOIN dsp_results d ON d.file_id=f.id AND d.source_revision=f.source_revision AND d.contract=j.semantic_key
		WHERE f.status='present' AND length(d.data)>0 ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return &frozenDSPSource{rows: rows}, nil
}

func (s *frozenDSPSource) Next(ctx context.Context) (librarylearn.DSPTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarylearn.DSPTrack{}, false, err
	}
	if !s.rows.Next() {
		return librarylearn.DSPTrack{}, false, s.rows.Err()
	}
	var id string
	var raw []byte
	if err := s.rows.Scan(&id, &raw); err != nil {
		return librarylearn.DSPTrack{}, false, err
	}
	var record DSPRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return librarylearn.DSPTrack{}, false, fmt.Errorf("track %s DSP: %w", id, err)
	}
	return dspLearningTrack(id, record), true, nil
}

func (s *frozenDSPSource) Close() error { return s.rows.Close() }

type dspAccessor struct {
	name string
	get  func(core.DSPFeatures) core.DSPValue
}

var dspAccessors = []dspAccessor{
	{"rms_dbfs", func(f core.DSPFeatures) core.DSPValue { return f.RMSDBFS }},
	{"sample_peak_dbfs", func(f core.DSPFeatures) core.DSPValue { return f.SamplePeakDBFS }},
	{"crest_factor_db", func(f core.DSPFeatures) core.DSPValue { return f.CrestFactorDB }},
	{"short_window_rms_spread_db", func(f core.DSPFeatures) core.DSPValue { return f.RMSWindowSpreadDB }},
	{"subbass_energy_ratio", func(f core.DSPFeatures) core.DSPValue { return f.SubbassEnergyRatio }},
	{"bass_energy_ratio", func(f core.DSPFeatures) core.DSPValue { return f.BassEnergyRatio }},
	{"treble_energy_ratio", func(f core.DSPFeatures) core.DSPValue { return f.TrebleEnergyRatio }},
	{"spectral_centroid_hz", func(f core.DSPFeatures) core.DSPValue { return f.SpectralCentroidHz }},
	{"positive_spectral_flux", func(f core.DSPFeatures) core.DSPValue { return f.PositiveSpectralFlux }},
	{"onset_rate_hz", func(f core.DSPFeatures) core.DSPValue { return f.OnsetRateHz }},
}

func dspLearningTrack(id string, record DSPRecord) librarylearn.DSPTrack {
	track := librarylearn.DSPTrack{TrackID: id, Contract: librarylearn.DSPContract{Version: record.Version, Sampling: record.Sampling, Scope: record.Scope}}
	for _, accessor := range dspAccessors {
		var weighted, knownDuration, totalDuration float64
		partial := false
		reasons := map[string]struct{}{}
		for _, window := range record.Windows {
			value := accessor.get(window.Features)
			if window.ObservedSeconds > 0 {
				totalDuration += window.ObservedSeconds
			}
			if value.Value != nil && window.ObservedSeconds > 0 {
				weighted += *value.Value * window.ObservedSeconds
				knownDuration += window.ObservedSeconds
			} else {
				partial = true
				reason := strings.TrimSpace(value.Reason)
				if reason == "" {
					reason = "not_observed"
				}
				reasons[reason] = struct{}{}
			}
		}
		feature := librarylearn.DSPFeatureValue{Name: accessor.name, Partial: partial || knownDuration < totalDuration}
		if knownDuration > 0 {
			value := weighted / knownDuration
			feature.Value = &value
		} else {
			ordered := make([]string, 0, len(reasons))
			for reason := range reasons {
				ordered = append(ordered, reason)
			}
			sort.Strings(ordered)
			if len(ordered) == 0 {
				ordered = append(ordered, "no_observed_windows")
			}
			feature.MissingReason = strings.Join(ordered, "+")
		}
		track.Features = append(track.Features, feature)
	}
	return track
}

type frozenSearchSource struct{ rows *sql.Rows }

func (s *frozenStore) searchSource(ctx context.Context) (*frozenSearchSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,v.vector
		FROM files f
		JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='audio' AND j.state='completed'
		JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=j.semantic_key
		WHERE f.status='present' AND length(v.vector)>0 ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return &frozenSearchSource{rows: rows}, nil
}

func (s *frozenSearchSource) Next(ctx context.Context) (librarysearch.VectorRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarysearch.VectorRow{}, false, err
	}
	if !s.rows.Next() {
		return librarysearch.VectorRow{}, false, s.rows.Err()
	}
	var id string
	var raw []byte
	if err := s.rows.Scan(&id, &raw); err != nil {
		return librarysearch.VectorRow{}, false, err
	}
	vector, err := decodeFloat32Vector(raw)
	if err != nil {
		return librarysearch.VectorRow{}, false, fmt.Errorf("track %s: %w", id, err)
	}
	return librarysearch.VectorRow{ID: id, Vector: vector}, true, nil
}

func (s *frozenSearchSource) Close() error { return s.rows.Close() }

func (s *frozenStore) diverseSample(ctx context.Context, dir string, limit int, seed uint64) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	temp, err := os.CreateTemp(dir, ".sample-*.sqlite")
	if err != nil {
		return nil, err
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	defer os.Remove(path)
	stage, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	if _, err := stage.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE; PRAGMA cache_size=-2048;
		CREATE TABLE candidates(group_id TEXT PRIMARY KEY, id TEXT NOT NULL, artist TEXT NOT NULL, group_rank BLOB NOT NULL, artist_rank BLOB NOT NULL, item_rank BLOB NOT NULL) WITHOUT ROWID;`); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,m.data
		FROM files f
		JOIN jobs jm ON jm.file_id=f.id AND jm.source_revision=f.source_revision AND jm.kind='metadata' AND jm.state='completed'
		JOIN track_metadata m ON m.file_id=f.id AND m.source_revision=f.source_revision AND m.contract=jm.semantic_key
		JOIN jobs ja ON ja.file_id=f.id AND ja.source_revision=f.source_revision AND ja.kind='audio' AND ja.state='completed'
		JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=ja.semantic_key
		WHERE f.status='present' AND length(v.vector)>0 ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tx, err := stage.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO candidates(group_id,id,artist,group_rank,artist_rank,item_rank) VALUES(?,?,?,?,?,?)
		ON CONFLICT(group_id) DO UPDATE SET id=excluded.id,artist=excluded.artist,group_rank=excluded.group_rank,artist_rank=excluded.artist_rank,item_rank=excluded.item_rank
		WHERE excluded.group_rank<candidates.group_rank OR (excluded.group_rank=candidates.group_rank AND excluded.id<candidates.id)`)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			_ = stmt.Close()
			return nil, err
		}
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			_ = stmt.Close()
			return nil, err
		}
		var record MetadataRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			_ = stmt.Close()
			return nil, fmt.Errorf("track %s metadata: %w", id, err)
		}
		item := sampleLearningItem(id, record)
		if item.ArtistID == "" {
			item.ArtistID = "unknown-artist:" + item.ID
		}
		if item.GroupID == "" {
			item.GroupID = "recording:" + item.ID
		}
		groupRank := keyedSamplingPriority(seed, "group", item.ID)
		artistRank := keyedSamplingPriority(seed, "artist", item.ArtistID)
		itemRank := keyedSamplingPriority(seed, "item", item.ID)
		if _, err := stmt.ExecContext(ctx, item.GroupID, item.ID, item.ArtistID, groupRank[:], artistRank[:], itemRank[:]); err != nil {
			_ = stmt.Close()
			return nil, err
		}
		count++
		if count%streamBatchRows == 0 {
			if err := stmt.Close(); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			tx, err = stage.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			stmt, err = tx.PrepareContext(ctx, `INSERT INTO candidates(group_id,id,artist,group_rank,artist_rank,item_rank) VALUES(?,?,?,?,?,?)
				ON CONFLICT(group_id) DO UPDATE SET id=excluded.id,artist=excluded.artist,group_rank=excluded.group_rank,artist_rank=excluded.artist_rank,item_rank=excluded.item_rank
				WHERE excluded.group_rank<candidates.group_rank OR (excluded.group_rank=candidates.group_rank AND excluded.id<candidates.id)`)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = stmt.Close()
		return nil, err
	}
	if err := stmt.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	selected, err := stage.QueryContext(ctx, `WITH ranked AS (
		SELECT id,artist,artist_rank,item_rank,ROW_NUMBER() OVER (PARTITION BY artist ORDER BY item_rank,id) AS round_no FROM candidates
	) SELECT id FROM ranked ORDER BY round_no,artist_rank,artist,item_rank,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer selected.Close()
	ids := make([]string, 0, limit)
	for selected.Next() {
		var id string
		if err := selected.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, selected.Err()
}

func keyedSamplingPriority(seed uint64, domain, id string) [32]byte {
	h := sha256.New()
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], seed)
	_, _ = h.Write([]byte(librarylearn.SamplingVersion))
	_, _ = h.Write(raw[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(id))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (s *frozenStore) loadSelectedVectors(ctx context.Context, ids []string) ([]librarylearn.DenseVector, error) {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	source, err := s.vectorSource(ctx)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	vectors := make([]librarylearn.DenseVector, 0, len(ids))
	for {
		row, ok, err := source.Next(ctx)
		if err != nil {
			clearDenseVectors(vectors)
			return nil, err
		}
		if !ok {
			break
		}
		if _, exists := wanted[row.ID]; exists {
			vectors = append(vectors, row)
		} else {
			clear(row.Values)
		}
	}
	if len(vectors) != len(ids) {
		clearDenseVectors(vectors)
		return nil, errors.New("library indexer: frozen training sample lost a vector")
	}
	return vectors, nil
}

func writeAssignmentStore(ctx context.Context, dir string, source *frozenVectorSource, model librarylearn.SphericalModel, workers int) (string, error) {
	temp, err := os.CreateTemp(dir, ".assignments-*.sqlite")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
		CREATE TABLE assignments(id TEXT PRIMARY KEY,cluster_id INTEGER NOT NULL,similarity REAL NOT NULL,alternative_cluster INTEGER NOT NULL,alternative_score REAL NOT NULL) WITHOUT ROWID;`); err != nil {
		_ = db.Close()
		return "", err
	}
	assignErr := librarylearn.AssignSphericalSource(ctx, source, model, librarylearn.AssignmentOptions{Workers: workers, LogicalBlock: 256, BatchSize: streamBatchRows}, func(batch []librarylearn.ClusterAssignment) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO assignments(id,cluster_id,similarity,alternative_cluster,alternative_score) VALUES(?,?,?,?,?)`)
		if err != nil {
			return err
		}
		for _, value := range batch {
			if _, err := stmt.ExecContext(ctx, value.ID, value.Cluster, value.Similarity, value.Alternative, value.AlternativeScore); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
		return tx.Commit()
	})
	if assignErr == nil {
		_, assignErr = db.ExecContext(ctx, "PRAGMA optimize")
	}
	if closeErr := db.Close(); assignErr == nil {
		assignErr = closeErr
	}
	if assignErr != nil {
		return "", assignErr
	}
	// The assignment store is app-created mutable state. Reopen it with write
	// access because Windows requires that right for FlushFileBuffers.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return "", err
	}
	target := filepath.Join(dir, assignmentStoreName)
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	keep = true
	if err := syncFileAndDirectory(target, dir); err != nil {
		return "", err
	}
	return assignmentStoreName, nil
}

type assignmentCursor struct {
	db      *sql.DB
	rows    *sql.Rows
	current librarylearn.ClusterAssignment
	has     bool
}

func openGenerationAssignments(ctx context.Context, dir, name string) (*assignmentCursor, error) {
	if name == "" {
		return nil, nil
	}
	if filepath.Base(name) != name || name != assignmentStoreName {
		return nil, errors.New("library indexer: unsafe assignment store path")
	}
	path := filepath.Join(dir, name)
	dsn, err := sqliteuri.ReadOnly(path, true)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, `SELECT id,cluster_id,similarity,alternative_cluster,alternative_score FROM assignments ORDER BY id`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	cursor := &assignmentCursor{db: db, rows: rows}
	if err := cursor.advance(); err != nil {
		_ = cursor.Close()
		return nil, err
	}
	return cursor, nil
}

func (c *assignmentCursor) advance() error {
	if !c.rows.Next() {
		c.has = false
		return c.rows.Err()
	}
	c.has = true
	return c.rows.Scan(&c.current.ID, &c.current.Cluster, &c.current.Similarity, &c.current.Alternative, &c.current.AlternativeScore)
}

func (c *assignmentCursor) For(trackID string) (librarylearn.ClusterAssignment, bool, error) {
	for c.has && c.current.ID < trackID {
		if err := c.advance(); err != nil {
			return librarylearn.ClusterAssignment{}, false, err
		}
	}
	if !c.has || c.current.ID != trackID {
		return librarylearn.ClusterAssignment{}, false, nil
	}
	value := c.current
	if err := c.advance(); err != nil {
		return librarylearn.ClusterAssignment{}, false, err
	}
	return value, true, nil
}

func (c *assignmentCursor) Close() error {
	return errors.Join(c.rows.Close(), c.db.Close())
}

func assignmentForTrack(cursor *assignmentCursor, legacy []librarylearn.ClusterAssignment, trackID string) (librarylearn.ClusterAssignment, bool, error) {
	if cursor != nil {
		return cursor.For(trackID)
	}
	index := sort.Search(len(legacy), func(i int) bool { return legacy[i].ID >= trackID })
	if index == len(legacy) || legacy[index].ID != trackID {
		return librarylearn.ClusterAssignment{}, false, nil
	}
	return legacy[index], true, nil
}

type frozenPackSource struct {
	store      *frozenStore
	rows       *sql.Rows
	cursor     *assignmentCursor
	legacy     []librarylearn.ClusterAssignment
	lastVector []float32
}

func openFrozenPackSource(ctx context.Context, snapshotPath string, cursor *assignmentCursor, legacy []librarylearn.ClusterAssignment) (*frozenPackSource, error) {
	store, err := openFrozenStore(snapshotPath)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT f.id,r.alias,f.relative_path,m.data,COALESCE(d.data,X''),COALESCE(v.vector,X''),
		CASE WHEN ja.state='failed' AND ja.error_code<>'unsupported' THEN ja.error_detail ELSE '' END,
		CASE WHEN ja.state='failed' AND ja.error_code='unsupported' THEN ja.error_detail ELSE '' END
		FROM files f JOIN roots r ON r.id=f.root_id
		JOIN jobs jm ON jm.file_id=f.id AND jm.source_revision=f.source_revision AND jm.kind='metadata' AND jm.state='completed'
		JOIN track_metadata m ON m.file_id=f.id AND m.source_revision=f.source_revision AND m.contract=jm.semantic_key
		LEFT JOIN jobs ja ON ja.file_id=f.id AND ja.source_revision=f.source_revision AND ja.kind='audio' AND ja.state<>'superseded'
		LEFT JOIN dsp_results d ON d.file_id=f.id AND d.source_revision=f.source_revision AND d.contract=ja.semantic_key
		LEFT JOIN mert_results v ON v.file_id=f.id AND v.source_revision=f.source_revision AND v.contract=ja.semantic_key
		WHERE f.status='present' ORDER BY f.id`)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &frozenPackSource{store: store, rows: rows, cursor: cursor, legacy: legacy}, nil
}

func (s *frozenPackSource) Next(ctx context.Context) (librarypack.Track, bool, error) {
	clear(s.lastVector)
	s.lastVector = nil
	if err := ctx.Err(); err != nil {
		return librarypack.Track{}, false, err
	}
	if !s.rows.Next() {
		return librarypack.Track{}, false, s.rows.Err()
	}
	var id, rootAlias, relativePath, failure, unsupported string
	var metadataRaw, dsp, vectorRaw []byte
	if err := s.rows.Scan(&id, &rootAlias, &relativePath, &metadataRaw, &dsp, &vectorRaw, &failure, &unsupported); err != nil {
		return librarypack.Track{}, false, err
	}
	var record MetadataRecord
	if err := json.Unmarshal(metadataRaw, &record); err != nil {
		return librarypack.Track{}, false, fmt.Errorf("track %s metadata: %w", id, err)
	}
	metadata := record.Probe.Metadata
	title, artist, album, albumArtist := "", "", "", ""
	missing := map[string]any{}
	if metadata.Title != nil {
		title = metadata.Title.Value
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(relativePath), filepath.Ext(relativePath))
		missing["title"] = map[string]string{"status": "fallback", "provenance": "filename"}
	}
	if values := tagValues(metadata.ArtistCredits); len(values) > 0 {
		artist = strings.Join(values, "; ")
	}
	if strings.TrimSpace(artist) == "" {
		artist = "Unknown artist"
		missing["artist"] = map[string]string{"status": "unknown", "provenance": "none"}
	}
	if metadata.Album != nil {
		album = metadata.Album.Value
	}
	if values := tagValues(metadata.AlbumArtists); len(values) > 0 {
		albumArtist = strings.Join(values, "; ")
	}
	rawTags, err := json.Marshal(metadata.RawTags)
	if err != nil {
		return librarypack.Track{}, false, err
	}
	missingness, err := json.Marshal(missing)
	if err != nil {
		return librarypack.Track{}, false, err
	}
	isrc := ""
	if metadata.ISRC != nil {
		isrc = normalizeISRC(metadata.ISRC.Value)
	}
	mbRecording := musicBrainzRecordingID(metadata.MusicBrainzIDs)
	acoustID := ""
	if metadata.AcoustID != nil {
		acoustID = strings.TrimSpace(metadata.AcoustID.Value)
	}
	packed := librarypack.Track{
		ID: id, Artist: artist, Title: title, NormalizedArtist: normalizeEntity(artist), NormalizedTitle: normalizeEntity(title),
		SourceIdentity: "library:" + id, ISRC: isrc, MusicBrainzRecording: mbRecording, AcoustID: acoustID,
		Album: album, AlbumArtist: albumArtist, RootAlias: rootAlias, RelativePath: filepath.ToSlash(relativePath),
		RawTags: rawTags, DSP: append(json.RawMessage(nil), dsp...), Missingness: missingness, Failure: failure, Unsupported: unsupported,
	}
	if fingerprint := record.AudioFingerprint.Value; record.AudioFingerprint.Status == "available" && fingerprint != nil {
		packed.AudioFingerprint = &librarypack.AudioFingerprint{
			Contract: fingerprint.Contract, Format: fingerprint.Format, Algorithm: fingerprint.Algorithm,
			Fingerprint: fingerprint.Fingerprint, FingerprintSHA256: fingerprint.FingerprintSHA256,
			Scope: fingerprint.Scope, DecoderRuntimeID: fingerprint.DecoderRuntimeID,
		}
	}
	packed.RecordingIdentity = librarypack.RecordingIdentity(packed)
	if record.Probe.Duration.Reliable && record.Probe.Duration.Seconds > 0 {
		packed.DurationMilliseconds = int64(math.Round(record.Probe.Duration.Seconds * 1000))
		packed.DurationProvenance = record.Probe.Duration.Provenance
		packed.DurationReliable = packed.DurationMilliseconds > 0
	}
	if len(vectorRaw) > 0 {
		packed.MERT, err = decodeFloat32Vector(vectorRaw)
		if err != nil {
			return librarypack.Track{}, false, fmt.Errorf("track %s: %w", id, err)
		}
		s.lastVector = packed.MERT
	}
	assignment, assigned, err := assignmentForTrack(s.cursor, s.legacy, id)
	if err != nil {
		return librarypack.Track{}, false, err
	}
	if assigned {
		packed.Cluster, packed.ClusterScore = &assignment.Cluster, assignment.Similarity
		packed.Alternative, packed.AltScore = &assignment.Alternative, assignment.AlternativeScore
	}
	return packed, true, nil
}

func musicBrainzRecordingID(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		normalized := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(key)))
		if normalized == "musicbrainz_trackid" || normalized == "musicbrainz_recordingid" {
			return strings.TrimSpace(values[key])
		}
	}
	return ""
}

func normalizeISRC(value string) string {
	trimmed := strings.TrimSpace(value)
	compact := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(trimmed))
	if len(compact) == 12 {
		valid := true
		for _, r := range compact {
			if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				valid = false
				break
			}
		}
		if valid {
			return compact
		}
	}
	return trimmed
}

func recordingIdentity(mbid, isrc, acoustID, artist, title string, fingerprint *librarypack.AudioFingerprint) string {
	return librarypack.RecordingIdentity(librarypack.Track{
		MusicBrainzRecording: mbid,
		ISRC:                 isrc,
		AcoustID:             acoustID,
		Artist:               artist,
		Title:                title,
		AudioFingerprint:     fingerprint,
	})
}

func metadataRecordingIdentity(record MetadataRecord) string {
	metadata := record.Probe.Metadata
	artist, title := "", ""
	if values := tagValues(metadata.ArtistCredits); len(values) > 0 {
		artist = strings.Join(values, "; ")
	}
	if metadata.Title != nil {
		title = metadata.Title.Value
	}
	mbid, isrc, acoustID := musicBrainzRecordingID(metadata.MusicBrainzIDs), "", ""
	if metadata.ISRC != nil {
		isrc = metadata.ISRC.Value
	}
	if metadata.AcoustID != nil {
		acoustID = metadata.AcoustID.Value
	}
	var fingerprint *librarypack.AudioFingerprint
	if value := record.AudioFingerprint.Value; record.AudioFingerprint.Status == "available" && value != nil {
		fingerprint = &librarypack.AudioFingerprint{Contract: value.Contract, FingerprintSHA256: value.FingerprintSHA256}
	}
	return recordingIdentity(mbid, isrc, acoustID, artist, title, fingerprint)
}

func (s *frozenPackSource) Close() error {
	clear(s.lastVector)
	return errors.Join(s.rows.Close(), s.store.Close())
}

func writeSphericalCheckpoint(path string, checkpoint librarylearn.SphericalCheckpoint) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".kmeans-checkpoint-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	encoder := json.NewEncoder(temp)
	if err = encoder.Encode(checkpoint); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncFileAndDirectory(path, dir)
}

func readSphericalCheckpoint(path string) (*librarylearn.SphericalCheckpoint, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var checkpoint librarylearn.SphericalCheckpoint
	if err := json.NewDecoder(file).Decode(&checkpoint); err != nil {
		return nil, fmt.Errorf("library indexer: invalid k-means checkpoint: %w", err)
	}
	return &checkpoint, nil
}

func effectiveTrainingSample(requested, vectors, dimension int, maxRAM int64) int {
	if vectors <= 0 || dimension <= 0 {
		return 0
	}
	if requested <= 0 {
		requested = 50_000
	}
	requested = min(requested, vectors)
	if maxRAM <= 0 {
		maxRAM = 2 << 30
	}
	// FitSpherical normalizes into an owned canonical copy while retaining the
	// selected source rows. Include both float32 copies plus conservative Go
	// slice/ID/assignment overhead. The remaining RAM is reserved for the
	// metadata model, centroids, fixed-block scratch, and process housekeeping.
	perVector := int64(dimension*8 + 256)
	byMemory := int(max(int64(1), (maxRAM/4)/perVector))
	return min(requested, byMemory)
}

func fitScratchBudget(maxRAM int64) int64 {
	if maxRAM <= 0 {
		return 256 << 20
	}
	return max(int64(32<<20), maxRAM/8)
}

func dspStatsScratchBudget(maxRAM int64) int64 {
	if maxRAM <= 0 {
		return 8 << 20
	}
	return max(int64(1<<20), min(int64(8<<20), maxRAM/64))
}

func indexScratchBudget(maxRAM int64) int64 {
	if maxRAM <= 0 {
		return 512 << 20
	}
	return max(int64(64<<20), maxRAM/4)
}

func indexShardRows(dimension int, budget int64) int {
	if dimension <= 0 || budget <= 0 {
		return 1
	}
	// librarysearch retains one source shard in addition to the shards owned by
	// workers, so a single shard may consume at most half this phase budget.
	rows := int((budget / 2) / int64(dimension*4+64))
	return max(1, min(16_384, rows))
}

func metadataLearningFields(id string, record MetadataRecord) librarylearn.MetadataTrack {
	metadata := record.Probe.Metadata
	artistValues := tagValues(metadata.ArtistCredits)
	albumArtistValues := tagValues(metadata.AlbumArtists)
	artistIDs := normalizedEntityIDs("artist", artistValues)
	albumArtistIDs := normalizedEntityIDs("artist", albumArtistValues)
	album := ""
	if metadata.Album != nil {
		album = normalizeEntity(metadata.Album.Value)
	}
	albumID := ""
	if album != "" {
		albumID = entityID("album", strings.Join(albumArtistValues, "\x00")+"\x00"+album)
	}
	return librarylearn.MetadataTrack{TrackID: id, AlbumID: albumID, ArtistIDs: artistIDs, AlbumArtistIDs: albumArtistIDs, Genres: tagValues(metadata.Genres)}
}

func sampleLearningItem(id string, record MetadataRecord) librarylearn.SampleItem {
	metadata := record.Probe.Metadata
	artist := ""
	if values := tagValues(metadata.ArtistCredits); len(values) > 0 {
		artist = entityID("artist", values[0])
	}
	group := ""
	if mbid := librarypack.CanonicalMusicBrainzRecordingID(musicBrainzRecordingID(metadata.MusicBrainzIDs)); mbid != "" {
		group = "musicbrainz:" + mbid
	} else if metadata.ISRC != nil && librarypack.CanonicalISRC(metadata.ISRC.Value) != "" {
		group = "isrc:" + librarypack.CanonicalISRC(metadata.ISRC.Value)
	} else if metadata.AcoustID != nil {
		acoustID := librarypack.CanonicalAcoustID(metadata.AcoustID.Value)
		if acoustID != "" {
			group = "acoustid-id:" + acoustID
		}
	}
	if group == "" {
		title := ""
		if metadata.Title != nil {
			title = metadata.Title.Value
		}
		group = entityID("probable-recording", artist+"\x00"+title)
	}
	return librarylearn.SampleItem{ID: id, ArtistID: artist, GroupID: group}
}

func vectorSpaceFromFields(metadata MetadataRecord, mertData []byte, dimension int) librarypack.VectorSpace {
	var record MERTRecord
	_ = json.Unmarshal(mertData, &record)
	return librarypack.VectorSpace{Name: "library_mert", Dimension: dimension, DType: "float32", ByteOrder: "little", Normalized: true,
		Model: record.Model.Model, ModelRevision: record.Model.Revision, GraphSHA256: record.Model.WeightsSHA256,
		Decoder: metadata.Probe.ProbeRuntimeID, Preprocessing: record.LocalPreprocessing, Sampling: record.Sampling,
		Pooling: record.Model.Pooling, Scope: "sampled_windows", Missingness: "absent rows have no vector; no zero placeholders"}
}

func clearDenseVectors(vectors []librarylearn.DenseVector) {
	for i := range vectors {
		clear(vectors[i].Values)
	}
}
