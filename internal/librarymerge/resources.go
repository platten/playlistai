package librarymerge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
)

type rebuiltResources struct {
	corpusGeneration     string
	metadataGeneration   string
	mertGeneration       string
	clusterGeneration    string
	statisticsGeneration string
	trainingSample       []string
	learning             json.RawMessage
	statistics           json.RawMessage
}

func (s *mergeStore) rebuildResources(ctx context.Context, vectorSpace librarypack.VectorSpace, options Options, membership string, inputs []*stagedInput) (rebuiltResources, error) {
	var resources rebuiltResources
	ids := sourcePackIDs(inputs)
	resources.corpusGeneration = generationID("merge", ids, options, membership)
	resources.metadataGeneration = generationID("metadata", ids, options, membership)
	var vectorCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM merged_tracks WHERE length(vector)>0`).Scan(&vectorCount); err != nil {
		return resources, err
	}
	if vectorCount > 0 {
		resources.mertGeneration = generationID("mert", ids, options, membership)
	}

	metadataSource, err := s.metadataSource(ctx)
	if err != nil {
		return resources, err
	}
	metadata, metadataErr := librarylearn.BuildMetadataSource(ctx, metadataSource, librarylearn.MetadataOptions{
		Workers: options.Workers, SVDDim: 64, SVDIterations: 4, MaxScratchBytes: max(int64(1<<20), options.MaxRAM/8),
	})
	closeErr := metadataSource.Close()
	if err := errors.Join(metadataErr, closeErr); err != nil {
		return resources, fmt.Errorf("librarymerge: rebuild metadata model: %w", err)
	}

	corpusTracks, err := s.countMergedTracks(ctx)
	if err != nil {
		return resources, err
	}
	dspSource, err := s.dspSource(ctx)
	if err != nil {
		return resources, err
	}
	statistics, statisticsErr := librarylearn.BuildDSPStatistics(ctx, dspSource, librarylearn.DSPStatisticsOptions{
		Seed: options.Seed, Workers: options.Workers, MaxSamplesPerFeature: 1024,
		MaxScratchBytes: max(int64(128<<10), min(int64(8<<20), options.MaxRAM/16)), CorpusTracks: corpusTracks,
	})
	closeErr = dspSource.Close()
	if err := errors.Join(statisticsErr, closeErr); err != nil {
		return resources, fmt.Errorf("librarymerge: rebuild DSP statistics: %w", err)
	}
	resources.statisticsGeneration = generationID("statistics", ids, options, membership)
	statistics.Generation = resources.statisticsGeneration

	effectiveSample := effectiveTrainingSample(options.TrainingSample, vectorCount, vectorSpace.Dimension, options.MaxRAM)
	resources.trainingSample, err = s.selectTrainingSample(ctx, effectiveSample, options.Seed)
	if err != nil {
		return resources, err
	}
	training, err := s.loadVectors(ctx, resources.trainingSample)
	if err != nil {
		return resources, err
	}
	defer clearVectors(training)
	var spherical *librarylearn.SphericalModel
	if len(training) >= 4 {
		clusters := options.Clusters
		if clusters <= 0 {
			clusters = librarylearn.RecommendedClusters(len(training))
		}
		clusters = min(clusters, len(training))
		model, err := librarylearn.FitSpherical(ctx, training, librarylearn.SphericalOptions{
			Clusters: clusters, BatchSize: min(1024, len(training)), LogicalBlock: 64, MaxEpochs: 20,
			Workers: options.Workers, Seed: options.Seed, Tolerance: 1e-5,
			MaxScratchBytes: max(int64(256<<10), options.MaxRAM/8), InputGeneration: resources.corpusGeneration,
		}, nil, nil)
		if err != nil {
			return resources, fmt.Errorf("librarymerge: rebuild spherical clusters: %w", err)
		}
		spherical = &model
		resources.clusterGeneration = generationID("clusters", ids, options, membership)
		if err := s.assignClusters(ctx, model, options.Workers); err != nil {
			return resources, err
		}
	}
	resources.learning, err = json.Marshal(struct {
		Version        int                          `json:"version"`
		Seed           uint64                       `json:"seed"`
		TrainingSample []string                     `json:"trainingSample"`
		Metadata       librarylearn.MetadataModel   `json:"metadata"`
		Spherical      *librarylearn.SphericalModel `json:"spherical,omitempty"`
	}{1, options.Seed, resources.trainingSample, metadata, spherical})
	if err != nil {
		return resources, err
	}
	resources.statistics, err = json.Marshal(statistics)
	if err != nil {
		return resources, err
	}
	return resources, nil
}

func (s *mergeStore) countMergedTracks(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM merged_tracks`).Scan(&count)
	return count, err
}

func effectiveTrainingSample(requested, vectors, dimension int, maxRAM int64) int {
	if requested <= 0 || vectors <= 0 || dimension <= 0 {
		return 0
	}
	perVector := int64(dimension*8 + 256)
	byMemory := int(max(int64(1), (maxRAM/4)/perVector))
	return min(requested, vectors, byMemory)
}

type metadataSource struct {
	rows   rowsCloser
	closed bool
}

type rowsCloser interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func (s *mergeStore) metadataSource(ctx context.Context) (*metadataSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT output_id,track_json FROM merged_tracks ORDER BY output_id`)
	if err != nil {
		return nil, err
	}
	return &metadataSource{rows: rows}, nil
}

func (s *metadataSource) Next(ctx context.Context) (librarylearn.MetadataTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarylearn.MetadataTrack{}, false, err
	}
	if s == nil || s.closed {
		return librarylearn.MetadataTrack{}, false, errors.New("librarymerge: metadata source is closed")
	}
	if !s.rows.Next() {
		return librarylearn.MetadataTrack{}, false, s.rows.Err()
	}
	var id string
	var raw []byte
	if err := s.rows.Scan(&id, &raw); err != nil {
		return librarylearn.MetadataTrack{}, false, err
	}
	var track librarypack.Track
	if err := json.Unmarshal(raw, &track); err != nil {
		return librarylearn.MetadataTrack{}, false, err
	}
	artistIDs := []string{entityID("artist", track.Artist)}
	var albumArtistIDs []string
	if strings.TrimSpace(track.AlbumArtist) != "" {
		albumArtistIDs = []string{entityID("artist", track.AlbumArtist)}
	}
	albumID := ""
	if strings.TrimSpace(track.Album) != "" {
		albumID = entityID("album", track.AlbumArtist+"\x00"+track.Album)
	}
	return librarylearn.MetadataTrack{
		TrackID: id, AlbumID: albumID, ArtistIDs: artistIDs, AlbumArtistIDs: albumArtistIDs, Genres: genresFromRawTags(track.RawTags),
	}, true, nil
}

func (s *metadataSource) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	return s.rows.Close()
}

func normalizeEntity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func entityID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + normalizeEntity(value)))
	return kind + ":" + hex.EncodeToString(sum[:12])
}

func genresFromRawTags(raw json.RawMessage) []string {
	var tags map[string]string
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), "genre") && strings.TrimSpace(tags[key]) != "" {
			return []string{tags[key]}
		}
	}
	return nil
}

type dspJSONValue struct {
	Value  *float64 `json:"value"`
	Reason string   `json:"reason,omitempty"`
}

type dspJSONRecord struct {
	Version  string `json:"version"`
	Sampling string `json:"sampling"`
	Scope    string `json:"scope"`
	Windows  []struct {
		ObservedSeconds float64                 `json:"observedSeconds"`
		Features        map[string]dspJSONValue `json:"features"`
	} `json:"windows"`
}

type dspSource struct {
	rows   rowsCloser
	closed bool
}

func (s *mergeStore) dspSource(ctx context.Context) (*dspSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT output_id,track_json FROM merged_tracks ORDER BY output_id`)
	if err != nil {
		return nil, err
	}
	return &dspSource{rows: rows}, nil
}

func (s *dspSource) Next(ctx context.Context) (librarylearn.DSPTrack, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return librarylearn.DSPTrack{}, false, err
		}
		if s == nil || s.closed {
			return librarylearn.DSPTrack{}, false, errors.New("librarymerge: DSP source is closed")
		}
		if !s.rows.Next() {
			return librarylearn.DSPTrack{}, false, s.rows.Err()
		}
		var id string
		var raw []byte
		if err := s.rows.Scan(&id, &raw); err != nil {
			return librarylearn.DSPTrack{}, false, err
		}
		var track librarypack.Track
		if err := json.Unmarshal(raw, &track); err != nil {
			return librarylearn.DSPTrack{}, false, err
		}
		if emptyJSONObject(track.DSP) {
			continue
		}
		var record dspJSONRecord
		if err := json.Unmarshal(track.DSP, &record); err != nil {
			return librarylearn.DSPTrack{}, false, fmt.Errorf("track %s DSP: %w", id, err)
		}
		result := librarylearn.DSPTrack{TrackID: id, Contract: librarylearn.DSPContract{Version: record.Version, Sampling: record.Sampling, Scope: record.Scope}}
		for _, name := range librarylearn.DSPFeatureNames {
			var weighted, knownDuration, totalDuration float64
			partial := false
			reasons := map[string]struct{}{}
			for _, window := range record.Windows {
				value, exists := window.Features[name]
				if window.ObservedSeconds > 0 {
					totalDuration += window.ObservedSeconds
				}
				if exists && value.Value != nil && window.ObservedSeconds > 0 {
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
			feature := librarylearn.DSPFeatureValue{Name: name, Partial: partial || knownDuration < totalDuration}
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
			result.Features = append(result.Features, feature)
		}
		return result, true, nil
	}
}

func (s *dspSource) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	return s.rows.Close()
}

func (s *mergeStore) selectTrainingSample(ctx context.Context, limit int, seed uint64) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE sample_candidates(id TEXT PRIMARY KEY,artist TEXT NOT NULL,artist_rank BLOB NOT NULL,item_rank BLOB NOT NULL) WITHOUT ROWID;`); err != nil {
		return nil, err
	}
	last := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT output_id,track_json FROM merged_tracks WHERE output_id>? AND length(vector)>0 ORDER BY output_id LIMIT ?`, last, mergeBatchRows)
		if err != nil {
			return nil, err
		}
		type candidate struct{ id, artist string }
		batch := make([]candidate, 0, mergeBatchRows)
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				_ = rows.Close()
				return nil, err
			}
			var track librarypack.Track
			if err := json.Unmarshal(raw, &track); err != nil {
				_ = rows.Close()
				return nil, err
			}
			artist := entityID("artist", track.Artist)
			batch = append(batch, candidate{id, artist})
			last = id
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO sample_candidates(id,artist,artist_rank,item_rank) VALUES(?,?,?,?)`)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		for _, item := range batch {
			artistRank, itemRank := keyedPriority(seed, "artist", item.artist), keyedPriority(seed, "item", item.id)
			if _, err := stmt.ExecContext(ctx, item.id, item.artist, artistRank[:], itemRank[:]); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return nil, err
			}
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `WITH ranked AS (
		SELECT id,artist,artist_rank,item_rank,ROW_NUMBER() OVER (PARTITION BY artist ORDER BY item_rank,id) AS round_no FROM sample_candidates
	) SELECT id FROM ranked ORDER BY round_no,artist_rank,artist,item_rank,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *mergeStore) loadVectors(ctx context.Context, ids []string) ([]librarylearn.DenseVector, error) {
	vectors := make([]librarylearn.DenseVector, 0, len(ids))
	for _, id := range ids {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT vector FROM merged_tracks WHERE output_id=? AND length(vector)>0`, id).Scan(&raw); err != nil {
			clearVectors(vectors)
			return nil, fmt.Errorf("librarymerge: load training vector %q: %w", id, err)
		}
		vector, err := decodeVector(raw)
		if err != nil {
			clearVectors(vectors)
			return nil, err
		}
		vectors = append(vectors, librarylearn.DenseVector{ID: id, Values: vector})
	}
	return vectors, nil
}

func clearVectors(vectors []librarylearn.DenseVector) {
	for index := range vectors {
		clear(vectors[index].Values)
	}
}

type pagedVectorSource struct {
	store *mergeStore
	last  string
	batch []librarylearn.DenseVector
	index int
	done  bool
}

func (s *pagedVectorSource) Next(ctx context.Context) (librarylearn.DenseVector, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarylearn.DenseVector{}, false, err
	}
	if s.index < len(s.batch) {
		row := s.batch[s.index]
		s.index++
		return row, true, nil
	}
	if s.done {
		return librarylearn.DenseVector{}, false, nil
	}
	s.batch, s.index = s.batch[:0], 0
	rows, err := s.store.db.QueryContext(ctx, `SELECT output_id,vector FROM merged_tracks WHERE output_id>? AND length(vector)>0 ORDER BY output_id LIMIT ?`, s.last, mergeBatchRows)
	if err != nil {
		return librarylearn.DenseVector{}, false, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return librarylearn.DenseVector{}, false, err
		}
		vector, err := decodeVector(raw)
		if err != nil {
			_ = rows.Close()
			return librarylearn.DenseVector{}, false, err
		}
		s.batch = append(s.batch, librarylearn.DenseVector{ID: id, Values: vector})
		s.last = id
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return librarylearn.DenseVector{}, false, err
	}
	if len(s.batch) == 0 {
		s.done = true
		return librarylearn.DenseVector{}, false, nil
	}
	row := s.batch[0]
	s.index = 1
	return row, true, nil
}

func (s *mergeStore) assignClusters(ctx context.Context, model librarylearn.SphericalModel, workers int) error {
	source := &pagedVectorSource{store: s}
	err := librarylearn.AssignSphericalSource(ctx, source, model, librarylearn.AssignmentOptions{Workers: workers, LogicalBlock: 256, BatchSize: mergeBatchRows}, func(batch []librarylearn.ClusterAssignment) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		stmt, err := tx.PrepareContext(ctx, `UPDATE merged_tracks SET cluster_id=?,cluster_score=?,alternative_cluster=?,alternative_score=? WHERE output_id=?`)
		if err != nil {
			return err
		}
		for _, assignment := range batch {
			if _, err := stmt.ExecContext(ctx, assignment.Cluster, assignment.Similarity, assignment.Alternative, assignment.AlternativeScore, assignment.ID); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
		return tx.Commit()
	})
	clearVectors(source.batch)
	if err != nil {
		return fmt.Errorf("librarymerge: assign spherical clusters: %w", err)
	}
	return nil
}
