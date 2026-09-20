package librarymerge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/platten/playlistai/internal/librarypack"
)

type mergeStore struct {
	db *sql.DB
}

func openMergeStore(ctx context.Context, path string) (*mergeStore, error) {
	db, err := openSQLite(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("librarymerge: open merge store: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE source_rows(
		row_id INTEGER PRIMARY KEY AUTOINCREMENT,
		pack_order INTEGER NOT NULL, track_order INTEGER NOT NULL,
		pack_id TEXT NOT NULL, original_id TEXT NOT NULL,
		isrc TEXT NOT NULL, mbid TEXT NOT NULL, acoustid TEXT NOT NULL,
		fingerprint_contract TEXT NOT NULL, fingerprint_sha256 TEXT NOT NULL,
		track_json BLOB NOT NULL, vector BLOB NOT NULL, parent INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX source_rows_isrc ON source_rows(isrc) WHERE isrc<>'';
	CREATE INDEX source_rows_mbid ON source_rows(mbid) WHERE mbid<>'';
	CREATE INDEX source_rows_acoustid ON source_rows(acoustid) WHERE acoustid<>'';
	CREATE INDEX source_rows_fingerprint ON source_rows(fingerprint_contract,fingerprint_sha256) WHERE fingerprint_sha256<>'';
	CREATE INDEX source_rows_parent ON source_rows(parent);`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("librarymerge: initialize merge store: %w", err)
	}
	return &mergeStore{db: db}, nil
}

func (s *mergeStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

type priorCandidate struct {
	rowID int64
	track librarypack.Track
}

func (s *mergeStore) ingest(ctx context.Context, input *stagedInput, aliases map[aliasKey]string, report *Report) error {
	source, err := input.staged.Generation().OpenTrackSource(ctx)
	if err != nil {
		return err
	}
	defer source.Close()
	var tx *sql.Tx
	var insert *sql.Stmt
	begin := func() error {
		var err error
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		insert, err = tx.PrepareContext(ctx, `INSERT INTO source_rows(pack_order,track_order,pack_id,original_id,isrc,mbid,acoustid,fingerprint_contract,fingerprint_sha256,track_json,vector) VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
		return err
	}
	finish := func(commit bool) error {
		if insert != nil {
			if err := insert.Close(); err != nil && commit {
				_ = tx.Rollback()
				return err
			}
			insert = nil
		}
		if tx == nil {
			return nil
		}
		if !commit {
			return tx.Rollback()
		}
		err := tx.Commit()
		tx = nil
		return err
	}
	if err := begin(); err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = finish(false)
		}
	}()
	trackOrder := 0
	for {
		track, ok, err := source.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if track.RootAlias != "" {
			alias, exists := aliases[aliasKey{input.manifest.PackID, track.RootAlias}]
			if !exists {
				return fmt.Errorf("root alias %q is missing from manifest", track.RootAlias)
			}
			track.RootAlias = alias
		}
		vector := encodeVector(track.MERT)
		track.MERT = nil
		track.Cluster, track.Alternative = nil, nil
		track.ClusterScore, track.AltScore = 0, 0
		candidates, matched, err := matchingCandidates(ctx, tx, track)
		if err != nil {
			return err
		}
		if matched["isrc"] {
			report.EvidenceMatches.ISRC++
		}
		if matched["musicbrainz"] {
			report.EvidenceMatches.MusicBrainz++
		}
		if matched["acoustid"] {
			report.EvidenceMatches.AcoustID++
		}
		if matched["fingerprint"] {
			report.EvidenceMatches.Fingerprint++
		}
		raw, err := json.Marshal(track)
		if err != nil {
			return err
		}
		fingerprintContract, fingerprintSHA := "", ""
		if track.AudioFingerprint != nil {
			fingerprintContract, fingerprintSHA = track.AudioFingerprint.Contract, track.AudioFingerprint.FingerprintSHA256
		}
		result, err := insert.ExecContext(ctx, input.order, trackOrder, input.manifest.PackID, track.ID,
			librarypack.CanonicalISRC(track.ISRC), librarypack.CanonicalMusicBrainzRecordingID(track.MusicBrainzRecording), librarypack.CanonicalAcoustID(track.AcoustID),
			fingerprintContract, fingerprintSHA, raw, vector)
		if err != nil {
			return err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE source_rows SET parent=? WHERE row_id=?`, rowID, rowID); err != nil {
			return err
		}
		for _, candidate := range candidates {
			if err := unionRows(ctx, tx, rowID, candidate.rowID); err != nil {
				return err
			}
		}
		trackOrder++
		if trackOrder%mergeBatchRows == 0 {
			if err := finish(true); err != nil {
				return err
			}
			if err := begin(); err != nil {
				return err
			}
		}
	}
	return finish(true)
}

func matchingCandidates(ctx context.Context, tx *sql.Tx, track librarypack.Track) ([]priorCandidate, map[string]bool, error) {
	byID := map[int64]priorCandidate{}
	matched := map[string]bool{}
	lookupOne := func(column, value, evidence string) error {
		if value == "" {
			return nil
		}
		var rowID int64
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT row_id,track_json FROM source_rows WHERE `+column+`=? ORDER BY row_id LIMIT 1`, value).Scan(&rowID, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var candidate librarypack.Track
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return err
		}
		byID[rowID] = priorCandidate{rowID: rowID, track: candidate}
		matched[evidence] = true
		return nil
	}
	if err := lookupOne("isrc", librarypack.CanonicalISRC(track.ISRC), "isrc"); err != nil {
		return nil, nil, err
	}
	if err := lookupOne("mbid", librarypack.CanonicalMusicBrainzRecordingID(track.MusicBrainzRecording), "musicbrainz"); err != nil {
		return nil, nil, err
	}
	if err := lookupOne("acoustid", librarypack.CanonicalAcoustID(track.AcoustID), "acoustid"); err != nil {
		return nil, nil, err
	}
	if track.AudioFingerprint != nil && track.AudioFingerprint.Contract != "" && track.AudioFingerprint.FingerprintSHA256 != "" {
		rows, err := tx.QueryContext(ctx, `SELECT row_id,track_json FROM source_rows WHERE fingerprint_contract=? AND fingerprint_sha256=? ORDER BY row_id`, track.AudioFingerprint.Contract, track.AudioFingerprint.FingerprintSHA256)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var rowID int64
			var raw []byte
			if err := rows.Scan(&rowID, &raw); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			var candidate librarypack.Track
			if err := json.Unmarshal(raw, &candidate); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			if fingerprintOnlyMatch(track, candidate) {
				byID[rowID] = priorCandidate{rowID: rowID, track: candidate}
				matched["fingerprint"] = true
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, err
		}
	}
	ids := make([]int64, 0, len(byID))
	for rowID := range byID {
		ids = append(ids, rowID)
	}
	sortInt64s(ids)
	out := make([]priorCandidate, 0, len(ids))
	for _, rowID := range ids {
		out = append(out, byID[rowID])
	}
	return out, matched, nil
}

func fingerprintOnlyMatch(left, right librarypack.Track) bool {
	left.ISRC, right.ISRC = "", ""
	left.MusicBrainzRecording, right.MusicBrainzRecording = "", ""
	left.AcoustID, right.AcoustID = "", ""
	return librarypack.SameRecording(left, right)
}

func sortInt64s(values []int64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func unionRows(ctx context.Context, tx *sql.Tx, left, right int64) error {
	leftRoot, err := findRoot(ctx, tx, left)
	if err != nil {
		return err
	}
	rightRoot, err := findRoot(ctx, tx, right)
	if err != nil {
		return err
	}
	if leftRoot == rightRoot {
		return nil
	}
	root, child := leftRoot, rightRoot
	if child < root {
		root, child = child, root
	}
	_, err = tx.ExecContext(ctx, `UPDATE source_rows SET parent=? WHERE row_id=?`, root, child)
	return err
}

func findRoot(ctx context.Context, tx *sql.Tx, rowID int64) (int64, error) {
	path := make([]int64, 0, 4)
	current := rowID
	for {
		var parent int64
		if err := tx.QueryRowContext(ctx, `SELECT parent FROM source_rows WHERE row_id=?`, current).Scan(&parent); err != nil {
			return 0, err
		}
		if parent == current {
			for _, child := range path {
				if _, err := tx.ExecContext(ctx, `UPDATE source_rows SET parent=? WHERE row_id=?`, current, child); err != nil {
					return 0, err
				}
			}
			return current, nil
		}
		path = append(path, current)
		current = parent
		if len(path) > 4096 {
			return 0, errors.New("librarymerge: invalid identity-group parent cycle")
		}
	}
}

func (s *mergeStore) resolveGroups(ctx context.Context) error {
	for iterations := 0; ; iterations++ {
		if iterations > 64 {
			return errors.New("librarymerge: identity groups did not converge")
		}
		result, err := s.db.ExecContext(ctx, `UPDATE source_rows AS child
			SET parent=(SELECT parent FROM source_rows AS direct WHERE direct.row_id=child.parent)
			WHERE child.parent<>(SELECT parent FROM source_rows AS direct WHERE direct.row_id=child.parent)`)
		if err != nil {
			return fmt.Errorf("librarymerge: resolve identity groups: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			break
		}
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE resolved_groups(row_id INTEGER PRIMARY KEY,root_id INTEGER NOT NULL) WITHOUT ROWID;
		INSERT INTO resolved_groups(row_id,root_id) SELECT row_id,parent FROM source_rows;
		CREATE INDEX resolved_groups_root ON resolved_groups(root_id,row_id);`)
	return err
}

func (s *mergeStore) mergeGroups(ctx context.Context, report *Report) (string, error) {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE merged_tracks(
		merge_id INTEGER PRIMARY KEY AUTOINCREMENT, root_id INTEGER NOT NULL UNIQUE,
		desired_id TEXT NOT NULL, output_id TEXT NOT NULL DEFAULT '', primary_pack_id TEXT NOT NULL,
		track_json BLOB NOT NULL, vector BLOB NOT NULL,
		cluster_id INTEGER, cluster_score REAL NOT NULL DEFAULT 0,
		alternative_cluster INTEGER, alternative_score REAL NOT NULL DEFAULT 0
	);`); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT g.root_id,r.row_id,r.pack_id,r.original_id,r.track_json,r.vector
		FROM source_rows r JOIN resolved_groups g ON g.row_id=r.row_id ORDER BY g.root_id,r.row_id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO merged_tracks(root_id,desired_id,primary_pack_id,track_json,vector) VALUES(?,?,?,?,?)`)
	if err != nil {
		return "", err
	}
	defer insert.Close()
	membership := sha256.New()
	var currentRoot int64 = -1
	var merged librarypack.Track
	var mergedVector []float32
	var primaryPack string
	groups := 0
	flush := func() error {
		if currentRoot < 0 {
			return nil
		}
		merged.MERT = nil
		merged.Cluster, merged.Alternative = nil, nil
		merged.ClusterScore, merged.AltScore = 0, 0
		merged.RecordingIdentity = librarypack.RecordingIdentity(merged)
		raw, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, currentRoot, merged.ID, primaryPack, raw, encodeVector(mergedVector)); err != nil {
			return err
		}
		groups++
		_, _ = membership.Write([]byte{0xff})
		return nil
	}
	for rows.Next() {
		var rootID, rowID int64
		var packID, originalID string
		var trackRaw, vectorRaw []byte
		if err := rows.Scan(&rootID, &rowID, &packID, &originalID, &trackRaw, &vectorRaw); err != nil {
			return "", err
		}
		var track librarypack.Track
		if err := json.Unmarshal(trackRaw, &track); err != nil {
			return "", err
		}
		vector, err := decodeVector(vectorRaw)
		if err != nil {
			return "", err
		}
		if rootID != currentRoot {
			if err := flush(); err != nil {
				return "", err
			}
			currentRoot, merged, mergedVector, primaryPack = rootID, track, vector, packID
		} else {
			mergeTrackEvidence(&merged, &mergedVector, track, vector, report.ConflictingFields)
		}
		_, _ = membership.Write([]byte(packID))
		_, _ = membership.Write([]byte{0})
		_, _ = membership.Write([]byte(originalID))
		_, _ = membership.Write([]byte{0})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if err := flush(); err != nil {
		return "", err
	}
	if err := rows.Close(); err != nil {
		return "", err
	}
	if err := insert.Close(); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	report.OutputTracks = groups
	report.RemovedDuplicates = report.InputTracks - groups
	return hex.EncodeToString(membership.Sum(nil)), nil
}

func mergeTrackEvidence(primary *librarypack.Track, primaryVector *[]float32, later librarypack.Track, laterVector []float32, conflicts map[string]int) {
	mergeString := func(name string, target *string, value string) {
		if strings.TrimSpace(*target) == "" && strings.TrimSpace(value) != "" {
			*target = value
		} else if strings.TrimSpace(*target) != "" && strings.TrimSpace(value) != "" && *target != value {
			conflicts[name]++
		}
	}
	mergeString("album", &primary.Album, later.Album)
	mergeString("album_artist", &primary.AlbumArtist, later.AlbumArtist)
	mergeString("isrc", &primary.ISRC, later.ISRC)
	mergeString("musicbrainz_recording", &primary.MusicBrainzRecording, later.MusicBrainzRecording)
	mergeString("acoustid", &primary.AcoustID, later.AcoustID)
	if primary.AudioFingerprint == nil && later.AudioFingerprint != nil {
		copy := *later.AudioFingerprint
		primary.AudioFingerprint = &copy
	} else if primary.AudioFingerprint != nil && later.AudioFingerprint != nil && !reflect.DeepEqual(*primary.AudioFingerprint, *later.AudioFingerprint) {
		conflicts["audio_fingerprint"]++
	}
	if primary.DurationMilliseconds == 0 && later.DurationMilliseconds > 0 {
		primary.DurationMilliseconds, primary.DurationProvenance, primary.DurationReliable = later.DurationMilliseconds, later.DurationProvenance, later.DurationReliable
	} else if primary.DurationMilliseconds > 0 && later.DurationMilliseconds > 0 && (primary.DurationMilliseconds != later.DurationMilliseconds || primary.DurationProvenance != later.DurationProvenance || primary.DurationReliable != later.DurationReliable) {
		conflicts["duration"]++
	}
	if primary.RelativePath == "" && later.RelativePath != "" {
		primary.RootAlias, primary.RelativePath = later.RootAlias, later.RelativePath
	} else if primary.RelativePath != "" && later.RelativePath != "" && (primary.RootAlias != later.RootAlias || primary.RelativePath != later.RelativePath) {
		conflicts["path"]++
	}
	if emptyJSONObject(primary.DSP) && !emptyJSONObject(later.DSP) {
		primary.DSP = append(json.RawMessage(nil), later.DSP...)
	} else if !emptyJSONObject(primary.DSP) && !emptyJSONObject(later.DSP) && !jsonEqual(primary.DSP, later.DSP) {
		conflicts["dsp"]++
	}
	if len(*primaryVector) == 0 && len(laterVector) > 0 {
		*primaryVector = append([]float32(nil), laterVector...)
	} else if len(*primaryVector) > 0 && len(laterVector) > 0 && !reflect.DeepEqual(*primaryVector, laterVector) {
		conflicts["mert"]++
	}
	if len(primary.CLAP) == 0 && len(later.CLAP) > 0 {
		primary.CLAP = append([]float32(nil), later.CLAP...)
		primary.CLAPEvidence = later.CLAPEvidence
	} else if len(primary.CLAP) > 0 && len(later.CLAP) > 0 && !reflect.DeepEqual(primary.CLAP, later.CLAP) {
		conflicts["clap"]++
	} else if primary.CLAPEvidence == nil && later.CLAPEvidence != nil {
		primary.CLAPEvidence = later.CLAPEvidence
	} else if primary.CLAPEvidence != nil && later.CLAPEvidence != nil && !reflect.DeepEqual(primary.CLAPEvidence, later.CLAPEvidence) {
		conflicts["clap_evidence"]++
	}
	primary.RawTags = mergeRawTags(primary.RawTags, later.RawTags, conflicts)
}

func emptyJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "{}" || trimmed == "null"
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func mergeRawTags(primary, later json.RawMessage, conflicts map[string]int) json.RawMessage {
	var left, right map[string]json.RawMessage
	leftErr, rightErr := json.Unmarshal(primary, &left), json.Unmarshal(later, &right)
	if leftErr != nil || rightErr != nil {
		if emptyJSONObject(primary) && !emptyJSONObject(later) {
			return append(json.RawMessage(nil), later...)
		}
		if !emptyJSONObject(primary) && !emptyJSONObject(later) && !jsonEqual(primary, later) {
			conflicts["raw_tags"]++
		}
		return primary
	}
	if left == nil {
		left = map[string]json.RawMessage{}
	}
	for key, value := range right {
		current, exists := left[key]
		if !exists || len(current) == 0 || string(current) == `""` {
			left[key] = append(json.RawMessage(nil), value...)
		} else if !jsonEqual(current, value) {
			conflicts["raw_tags"]++
		}
	}
	raw, err := json.Marshal(left)
	if err != nil {
		return primary
	}
	return raw
}

func (s *mergeStore) assignTrackIDs(ctx context.Context, report *Report) error {
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX merged_tracks_desired ON merged_tracks(desired_id,merge_id);
		UPDATE merged_tracks SET output_id=desired_id WHERE desired_id IN (SELECT desired_id FROM merged_tracks GROUP BY desired_id HAVING COUNT(*)=1);
		CREATE INDEX merged_tracks_output_pending ON merged_tracks(output_id);`); err != nil {
		return err
	}
	type rewrite struct {
		mergeID          int64
		original, packID string
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT merge_id,desired_id,primary_pack_id FROM merged_tracks WHERE output_id='' ORDER BY desired_id,merge_id LIMIT ?`, mergeBatchRows)
		if err != nil {
			return err
		}
		batch := make([]rewrite, 0, mergeBatchRows)
		for rows.Next() {
			var item rewrite
			if err := rows.Scan(&item.mergeID, &item.original, &item.packID); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, item := range batch {
			candidate, err := suffixedTrackID(ctx, tx, item.original, item.packID)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE merged_tracks SET output_id=? WHERE merge_id=?`, candidate, item.mergeID); err != nil {
				_ = tx.Rollback()
				return err
			}
			report.TrackIDRewrites++
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DROP INDEX merged_tracks_output_pending; CREATE UNIQUE INDEX merged_tracks_output ON merged_tracks(output_id)`)
	return err
}

func suffixedTrackID(ctx context.Context, tx *sql.Tx, base, identity string) (string, error) {
	available := func(candidate string) (bool, error) {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM merged_tracks WHERE output_id=? LIMIT 1)`, candidate).Scan(&exists); err != nil {
			return false, err
		}
		return exists == 0, nil
	}
	for length := 12; length <= len(identity); length += 4 {
		suffix := identity[:min(length, len(identity))]
		prefix := base
		if len(prefix)+1+len(suffix) > 256 {
			prefix = prefix[:256-1-len(suffix)]
		}
		candidate := prefix + "-" + suffix
		if ok, err := available(candidate); err != nil {
			return "", err
		} else if ok {
			return candidate, nil
		}
	}
	digest := sha256.Sum256([]byte(identity + "\x00" + base))
	encoded := hex.EncodeToString(digest[:])
	for length := 12; length <= len(encoded); length += 4 {
		suffix := encoded[:length]
		prefix := base
		if len(prefix)+1+len(suffix) > 256 {
			prefix = prefix[:256-1-len(suffix)]
		}
		candidate := prefix + "-" + suffix
		if ok, err := available(candidate); err != nil {
			return "", err
		} else if ok {
			return candidate, nil
		}
	}
	return "", errors.New("librarymerge: could not derive a unique bounded track ID")
}

type outputTrackSource struct {
	rows *sql.Rows
}

func (s *mergeStore) outputSource(ctx context.Context) (*outputTrackSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT output_id,track_json,vector,cluster_id,cluster_score,alternative_cluster,alternative_score FROM merged_tracks ORDER BY output_id`)
	if err != nil {
		return nil, err
	}
	return &outputTrackSource{rows: rows}, nil
}

func (s *outputTrackSource) Next(ctx context.Context) (librarypack.Track, bool, error) {
	if err := ctx.Err(); err != nil {
		return librarypack.Track{}, false, err
	}
	if s == nil || s.rows == nil {
		return librarypack.Track{}, false, errors.New("librarymerge: output source is closed")
	}
	if !s.rows.Next() {
		return librarypack.Track{}, false, s.rows.Err()
	}
	var id string
	var raw, vectorRaw []byte
	var cluster, alternative sql.NullInt64
	var clusterScore, alternativeScore float64
	if err := s.rows.Scan(&id, &raw, &vectorRaw, &cluster, &clusterScore, &alternative, &alternativeScore); err != nil {
		return librarypack.Track{}, false, err
	}
	var track librarypack.Track
	if err := json.Unmarshal(raw, &track); err != nil {
		return librarypack.Track{}, false, err
	}
	track.ID = id
	vector, err := decodeVector(vectorRaw)
	if err != nil {
		return librarypack.Track{}, false, err
	}
	track.MERT = vector
	if cluster.Valid {
		value := int(cluster.Int64)
		track.Cluster, track.ClusterScore = &value, clusterScore
	}
	if alternative.Valid {
		value := int(alternative.Int64)
		track.Alternative, track.AltScore = &value, alternativeScore
	}
	return track, true, nil
}

func (s *outputTrackSource) Close() error {
	if s == nil || s.rows == nil {
		return nil
	}
	err := s.rows.Close()
	s.rows = nil
	return err
}
