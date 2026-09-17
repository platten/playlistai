package librarypack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/platten/playlistai/internal/librarylearn"
)

const (
	maxMetadataVocabulary = 65_536
	maxMetadataRowValues  = 4_096
	maxSVDDimensions      = 256
	maxSphericalClusters  = 4_096
	maxDSPGroups          = 128
	maxDSPFeatures        = 1_024
)

type portableLearning struct {
	Version        int                          `json:"version"`
	Seed           uint64                       `json:"seed"`
	TrainingSample []string                     `json:"trainingSample"`
	Metadata       librarylearn.MetadataModel   `json:"metadata"`
	Spherical      *librarylearn.SphericalModel `json:"spherical,omitempty"`
}

func writeNormalizedResources(ctx context.Context, tx *sql.Tx, learning, statistics json.RawMessage) error {
	var model portableLearning
	if string(learning) != "{}" {
		if err := json.Unmarshal(learning, &model); err != nil {
			return fmt.Errorf("librarypack: decode learned resources: %w", err)
		}
	}
	info := []struct{ key, value string }{{"version", strconv.Itoa(model.Version)}, {"seed", strconv.FormatUint(model.Seed, 10)}, {"metadata_version", model.Metadata.Version}}
	for _, item := range info {
		if _, err := tx.ExecContext(ctx, "INSERT INTO learning_info(key,value) VALUES(?,?)", item.key, item.value); err != nil {
			return err
		}
	}
	for position, id := range model.TrainingSample {
		if _, err := tx.ExecContext(ctx, "INSERT INTO training_sample(position,id) VALUES(?,?)", position, id); err != nil {
			return err
		}
	}
	for column, term := range model.Metadata.Vocabulary {
		idf := float64(0)
		if column < len(model.Metadata.IDF) {
			idf = model.Metadata.IDF[column]
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO metadata_vocabulary(column_id,term,idf) VALUES(?,?,?)", column, term, idf); err != nil {
			return err
		}
	}
	for _, row := range model.Metadata.Rows {
		for _, value := range row.Values {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata_rows(artist_id,column_id,value) VALUES(?,?,?)", row.ArtistID, value.Column, value.Value); err != nil {
				return err
			}
		}
	}
	for _, association := range model.Metadata.Associations {
		if _, err := tx.ExecContext(ctx, "INSERT INTO metadata_associations(artist_id,album_id,role) VALUES(?,?,?)", association.ArtistID, association.AlbumID, association.Role); err != nil {
			return err
		}
	}
	svd := model.Metadata.SVD
	if _, err := tx.ExecContext(ctx, "INSERT INTO svd_model(version,outcome,reason,dimension,columns_count) VALUES(?,?,?,?,?)", svd.Version, string(svd.Outcome), svd.Reason, svd.Dimension, svd.Columns); err != nil {
		return err
	}
	for component, value := range svd.SingularValues {
		if _, err := tx.ExecContext(ctx, "INSERT INTO svd_values(kind,row_id,column_id,value) VALUES('singular',?,?,?)", component, 0, value); err != nil {
			return err
		}
	}
	for offset, value := range svd.Components {
		row, column := 0, 0
		if svd.Columns > 0 {
			row, column = offset/svd.Columns, offset%svd.Columns
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO svd_values(kind,row_id,column_id,value) VALUES('component',?,?,?)", row, column, value); err != nil {
			return err
		}
	}
	if spherical := model.Spherical; spherical != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO spherical_model(version,input_generation,input_digest,seed,dimension,clusters,epochs,objective) VALUES(?,?,?,?,?,?,?,?)`, spherical.Version, spherical.InputGeneration, spherical.InputDigest, strconv.FormatUint(spherical.Seed, 10), spherical.Dimension, spherical.Clusters, spherical.Epochs, spherical.Objective); err != nil {
			return err
		}
		for offset, value := range spherical.Centroids {
			row, column := 0, 0
			if spherical.Dimension > 0 {
				row, column = offset/spherical.Dimension, offset%spherical.Dimension
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO spherical_values(kind,row_id,column_id,value) VALUES('centroid',?,?,?)", row, column, value); err != nil {
				return err
			}
		}
		for cluster, count := range spherical.Counts {
			if _, err := tx.ExecContext(ctx, "INSERT INTO spherical_values(kind,row_id,column_id,value) VALUES('count',?,?,?)", cluster, 0, float64(count)); err != nil {
				return err
			}
		}
	}
	var dsp librarylearn.DSPStatisticsModel
	if string(statistics) != "{}" {
		if err := json.Unmarshal(statistics, &dsp); err != nil {
			return fmt.Errorf("librarypack: decode DSP statistics: %w", err)
		}
	}
	statisticsInfo := []struct{ key, value string }{
		{"version", dsp.Version}, {"generation", dsp.Generation}, {"quantile_method", dsp.QuantileMethod},
		{"seed", strconv.FormatUint(dsp.Seed, 10)}, {"corpus_tracks", strconv.Itoa(dsp.CorpusTracks)},
		{"dsp_tracks", strconv.Itoa(dsp.DSPTracks)}, {"missing_dsp", strconv.Itoa(dsp.MissingDSP)},
	}
	for _, item := range statisticsInfo {
		if _, err := tx.ExecContext(ctx, "INSERT INTO dsp_statistics_info(key,value) VALUES(?,?)", item.key, item.value); err != nil {
			return err
		}
	}
	for groupID, group := range dsp.Groups {
		if _, err := tx.ExecContext(ctx, "INSERT INTO dsp_statistics_groups(group_id,id,version,sampling,scope,tracks) VALUES(?,?,?,?,?,?)", groupID, group.ID, group.Contract.Version, group.Contract.Sampling, group.Contract.Scope, group.Tracks); err != nil {
			return err
		}
		for position, feature := range group.Features {
			raw, err := json.Marshal(feature)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO dsp_statistics_features(group_id,position,name,summary_json) VALUES(?,?,?,?)", groupID, position, feature.Name, string(raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Generation) validateNormalizedResources(ctx context.Context, limits Limits) error {
	if g == nil || g.db == nil {
		return errors.New("librarypack: generation is closed")
	}
	boundedCount := func(query, label string, maximum int64) error {
		var count int64
		if err := g.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count < 0 || count > maximum {
			return fmt.Errorf("librarypack: normalized resource %s exceeds its row limit", label)
		}
		return nil
	}
	if err := boundedCount("SELECT COUNT(*) FROM metadata_vocabulary", "metadata_vocabulary", maxMetadataVocabulary); err != nil {
		return err
	}
	if err := boundedCount("SELECT COUNT(*) FROM training_sample", "training_sample", int64(limits.MaxTracks)); err != nil {
		return err
	}
	if err := boundedCount("SELECT COUNT(*) FROM metadata_associations", "metadata_associations", int64(limits.MaxTracks)*64); err != nil {
		return err
	}
	if err := boundedCount("SELECT COUNT(*) FROM dsp_statistics_groups", "dsp_statistics_groups", maxDSPGroups); err != nil {
		return err
	}
	if err := boundedCount("SELECT COUNT(*) FROM dsp_statistics_features", "dsp_statistics_features", maxDSPFeatures); err != nil {
		return err
	}
	var oversized int
	if err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_rows GROUP BY artist_id HAVING COUNT(*)>? LIMIT 1)`, maxMetadataRowValues).Scan(&oversized); err != nil {
		return err
	}
	if oversized != 0 {
		return errors.New("librarypack: normalized artist row exceeds its value limit")
	}
	var vocabulary, dimension, columns int64
	if err := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata_vocabulary").Scan(&vocabulary); err != nil {
		return err
	}
	if err := g.db.QueryRowContext(ctx, "SELECT dimension,columns_count FROM svd_model").Scan(&dimension, &columns); err != nil {
		return err
	}
	if dimension < 0 || dimension > maxSVDDimensions || columns != vocabulary && (dimension != 0 || columns != 0) {
		return errors.New("librarypack: normalized SVD shape is invalid")
	}
	expectedSVDValues := dimension*columns + dimension
	if err := boundedCount("SELECT COUNT(*) FROM svd_values", "svd_values", expectedSVDValues); err != nil {
		return err
	}
	var svdValues int64
	if err := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM svd_values").Scan(&svdValues); err != nil || svdValues != expectedSVDValues {
		return errors.New("librarypack: normalized SVD values do not match the declared shape")
	}
	var clusters, sphericalDimension int64
	err := g.db.QueryRowContext(ctx, "SELECT clusters,dimension FROM spherical_model").Scan(&clusters, &sphericalDimension)
	if err == nil {
		if clusters <= 0 || clusters > maxSphericalClusters || sphericalDimension <= 0 || sphericalDimension > int64(limits.MaxVectorDim) {
			return errors.New("librarypack: normalized spherical model shape is invalid")
		}
		expectedSphericalValues := clusters*sphericalDimension + clusters
		if err := boundedCount("SELECT COUNT(*) FROM spherical_values", "spherical_values", expectedSphericalValues); err != nil {
			return err
		}
		var sphericalValues int64
		if err := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM spherical_values").Scan(&sphericalValues); err != nil || sphericalValues != expectedSphericalValues {
			return errors.New("librarypack: normalized spherical values do not match the declared shape")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var dangling int
	if err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_rows r LEFT JOIN metadata_vocabulary v ON v.column_id=r.column_id WHERE v.column_id IS NULL LIMIT 1)`).Scan(&dangling); err != nil {
		return err
	}
	if dangling != 0 {
		return errors.New("librarypack: sparse metadata row references an unknown vocabulary column")
	}
	return nil
}

// MetadataModel reconstructs the complete normalized metadata resource for
// compatibility/export callers. Interactive catalog readers should use
// MetadataBasis plus MetadataRow so corpus-sized sparse artist rows remain on
// disk and are fetched only for bounded candidates.
func (g *Generation) MetadataModel(ctx context.Context) (librarylearn.MetadataModel, error) {
	return g.metadataModel(ctx, true)
}

// MetadataBasis loads vocabulary/IDF and the bounded SVD basis without
// materializing corpus-sized artist rows or album associations.
func (g *Generation) MetadataBasis(ctx context.Context) (librarylearn.MetadataModel, error) {
	return g.metadataModel(ctx, false)
}

func (g *Generation) metadataModel(ctx context.Context, includeCorpusRows bool) (librarylearn.MetadataModel, error) {
	var model librarylearn.MetadataModel
	if g == nil || g.db == nil {
		return model, errors.New("librarypack: generation is closed")
	}
	_ = g.db.QueryRowContext(ctx, "SELECT value FROM learning_info WHERE key='metadata_version'").Scan(&model.Version)
	rows, err := g.db.QueryContext(ctx, "SELECT column_id,term,idf FROM metadata_vocabulary ORDER BY column_id")
	if err != nil {
		return model, err
	}
	for rows.Next() {
		var column int
		var term string
		var idf float64
		if err := rows.Scan(&column, &term, &idf); err != nil || column != len(model.Vocabulary) {
			_ = rows.Close()
			if err != nil {
				return model, err
			}
			return model, errors.New("librarypack: noncanonical metadata vocabulary")
		}
		model.Vocabulary, model.IDF = append(model.Vocabulary, term), append(model.IDF, idf)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return model, err
	}
	if includeCorpusRows {
		rows, err = g.db.QueryContext(ctx, "SELECT artist_id,column_id,value FROM metadata_rows ORDER BY artist_id,column_id")
		if err != nil {
			return model, err
		}
		for rows.Next() {
			var artist string
			var value librarylearn.SparseValue
			if err := rows.Scan(&artist, &value.Column, &value.Value); err != nil {
				_ = rows.Close()
				return model, err
			}
			if len(model.Rows) == 0 || model.Rows[len(model.Rows)-1].ArtistID != artist {
				model.Rows = append(model.Rows, librarylearn.SparseRow{ArtistID: artist})
			}
			model.Rows[len(model.Rows)-1].Values = append(model.Rows[len(model.Rows)-1].Values, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return model, err
		}
		rows, err = g.db.QueryContext(ctx, "SELECT artist_id,album_id,role FROM metadata_associations ORDER BY artist_id,album_id,role")
		if err != nil {
			return model, err
		}
		for rows.Next() {
			var association librarylearn.ArtistAlbumAssociation
			if err := rows.Scan(&association.ArtistID, &association.AlbumID, &association.Role); err != nil {
				_ = rows.Close()
				return model, err
			}
			model.Associations = append(model.Associations, association)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return model, err
		}
	}
	var outcome string
	if err := g.db.QueryRowContext(ctx, "SELECT version,outcome,reason,dimension,columns_count FROM svd_model").Scan(&model.SVD.Version, &outcome, &model.SVD.Reason, &model.SVD.Dimension, &model.SVD.Columns); err != nil {
		return model, err
	}
	model.SVD.Outcome = librarylearn.SVDOutcome(outcome)
	rows, err = g.db.QueryContext(ctx, "SELECT kind,row_id,column_id,value FROM svd_values ORDER BY kind,row_id,column_id")
	if err != nil {
		return model, err
	}
	for rows.Next() {
		var kind string
		var row, column int
		var value float64
		if err := rows.Scan(&kind, &row, &column, &value); err != nil {
			_ = rows.Close()
			return model, err
		}
		if kind == "singular" {
			model.SVD.SingularValues = append(model.SVD.SingularValues, value)
		} else {
			model.SVD.Components = append(model.SVD.Components, value)
		}
	}
	return model, errors.Join(rows.Err(), rows.Close())
}

// MetadataRow performs a bounded point lookup in the normalized sparse model.
func (g *Generation) MetadataRow(ctx context.Context, artistID string) (librarylearn.SparseRow, bool, error) {
	row := librarylearn.SparseRow{ArtistID: artistID}
	if g == nil || g.db == nil || artistID == "" {
		return row, false, errors.New("librarypack: generation is closed or artist identity is empty")
	}
	rows, err := g.db.QueryContext(ctx, "SELECT column_id,value FROM metadata_rows WHERE artist_id=? ORDER BY column_id", artistID)
	if err != nil {
		return row, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var value librarylearn.SparseValue
		if err := rows.Scan(&value.Column, &value.Value); err != nil {
			return row, false, err
		}
		row.Values = append(row.Values, value)
	}
	if err := rows.Err(); err != nil {
		return row, false, err
	}
	return row, len(row.Values) > 0, nil
}

func (g *Generation) normalizedLearning(ctx context.Context) (json.RawMessage, error) {
	metadata, err := g.MetadataModel(ctx)
	if err != nil {
		return nil, err
	}
	var version int
	var seedRaw string
	_ = g.db.QueryRowContext(ctx, "SELECT value FROM learning_info WHERE key='version'").Scan(&version)
	_ = g.db.QueryRowContext(ctx, "SELECT value FROM learning_info WHERE key='seed'").Scan(&seedRaw)
	seed, _ := strconv.ParseUint(seedRaw, 10, 64)
	payload := portableLearning{Version: version, Seed: seed, Metadata: metadata}
	rows, err := g.db.QueryContext(ctx, "SELECT id FROM training_sample ORDER BY position")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		payload.TrainingSample = append(payload.TrainingSample, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var spherical librarylearn.SphericalModel
	var sphericalSeed string
	err = g.db.QueryRowContext(ctx, "SELECT version,input_generation,input_digest,seed,dimension,clusters,epochs,objective FROM spherical_model").Scan(&spherical.Version, &spherical.InputGeneration, &spherical.InputDigest, &sphericalSeed, &spherical.Dimension, &spherical.Clusters, &spherical.Epochs, &spherical.Objective)
	if err == nil {
		spherical.Seed, _ = strconv.ParseUint(sphericalSeed, 10, 64)
		values, queryErr := g.db.QueryContext(ctx, "SELECT kind,row_id,column_id,value FROM spherical_values ORDER BY kind,row_id,column_id")
		if queryErr != nil {
			return nil, queryErr
		}
		for values.Next() {
			var kind string
			var row, column int
			var value float64
			if scanErr := values.Scan(&kind, &row, &column, &value); scanErr != nil {
				_ = values.Close()
				return nil, scanErr
			}
			if kind == "centroid" {
				spherical.Centroids = append(spherical.Centroids, float32(value))
			} else {
				spherical.Counts = append(spherical.Counts, uint64(value))
			}
		}
		if err := errors.Join(values.Err(), values.Close()); err != nil {
			return nil, err
		}
		payload.Spherical = &spherical
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	return json.RawMessage(raw), err
}

func (g *Generation) normalizedStatistics(ctx context.Context) (json.RawMessage, bool, error) {
	info := map[string]string{}
	rows, err := g.db.QueryContext(ctx, "SELECT key,value FROM dsp_statistics_info")
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		info[key] = value
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, false, err
	}
	if info["generation"] == "" {
		return nil, false, nil
	}
	seed, _ := strconv.ParseUint(info["seed"], 10, 64)
	corpus, _ := strconv.Atoi(info["corpus_tracks"])
	dspTracks, _ := strconv.Atoi(info["dsp_tracks"])
	missing, _ := strconv.Atoi(info["missing_dsp"])
	model := librarylearn.DSPStatisticsModel{Version: info["version"], Generation: info["generation"], QuantileMethod: info["quantile_method"], Seed: seed, CorpusTracks: corpus, DSPTracks: dspTracks, MissingDSP: missing}
	groups, err := g.db.QueryContext(ctx, "SELECT group_id,id,version,sampling,scope,tracks FROM dsp_statistics_groups ORDER BY group_id")
	if err != nil {
		return nil, false, err
	}
	for groups.Next() {
		var groupID int
		var group librarylearn.DSPStatisticsGroup
		if err := groups.Scan(&groupID, &group.ID, &group.Contract.Version, &group.Contract.Sampling, &group.Contract.Scope, &group.Tracks); err != nil {
			_ = groups.Close()
			return nil, false, err
		}
		features, err := g.db.QueryContext(ctx, "SELECT summary_json FROM dsp_statistics_features WHERE group_id=? ORDER BY position", groupID)
		if err != nil {
			_ = groups.Close()
			return nil, false, err
		}
		for features.Next() {
			var raw string
			var feature librarylearn.DSPFeatureStatistics
			if err := features.Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &feature) != nil {
				_ = features.Close()
				_ = groups.Close()
				return nil, false, errors.New("librarypack: invalid normalized DSP feature")
			}
			group.Features = append(group.Features, feature)
		}
		if err := errors.Join(features.Err(), features.Close()); err != nil {
			_ = groups.Close()
			return nil, false, err
		}
		model.Groups = append(model.Groups, group)
	}
	if err := errors.Join(groups.Err(), groups.Close()); err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(model)
	return json.RawMessage(raw), true, err
}
