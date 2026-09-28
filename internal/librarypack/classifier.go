package librarypack

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"github.com/platten/playlistai/internal/core"
)

// Classifier evidence is an optional checksummed metadata table. It never adds
// a factual capability or turns a score into a calibrated requirement match.
func classifierEvidenceJSON(track Track, clapBytes int, limits Limits) ([]byte, error) {
	if len(track.ClassifierEvidence) == 0 {
		return nil, nil
	}
	if err := validateClassifierEvidence(track.ClassifierEvidence, track.DurationMilliseconds); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(track.ClassifierEvidence)
	if err != nil {
		return nil, err
	}
	if len(raw) > limits.MaxJSONBytes || len(raw)+clapBytes+classifierMetadataBytes(track) > limits.MaxRecordBytes {
		return nil, errors.New("librarypack: classifier evidence allocation limit exceeded")
	}
	return raw, nil
}

func classifierMetadataBytes(track Track) int {
	capabilities, _ := json.Marshal(track.Capabilities)
	size := len(track.ID) + len(track.Artist) + len(track.Title) + len(track.NormalizedArtist) + len(track.NormalizedTitle) + len(track.SourceIdentity) + len(track.RecordingIdentity) + len(track.ISRC) + len(track.MusicBrainzRecording) + len(track.AcoustID) + len(track.DurationProvenance) + len(track.AlbumArtist) + len(track.Album) + len(track.RootAlias) + len(track.RelativePath) + len(track.Failure) + len(track.Unsupported) + len(track.RawTags) + len(track.DSP) + len(track.Missingness) + len(capabilities)
	if f := track.AudioFingerprint; f != nil {
		size += len(f.Contract) + len(f.Format) + len(f.Fingerprint) + len(f.FingerprintSHA256) + len(f.Scope) + len(f.DecoderRuntimeID)
	}
	return size
}

func validateClassifierEvidence(evidence []core.MusicClassifierEvidence, durationMS int64) error {
	if len(evidence) == 0 || len(evidence) > 8 {
		return errors.New("librarypack: expected 1–8 classifier observations")
	}
	seen := map[string]bool{}
	for _, entry := range evidence {
		if err := entry.Validate(); err != nil {
			return err
		}
		key := entry.Fingerprint() + "/" + entry.AudioSHA256
		if seen[key] {
			return errors.New("librarypack: duplicate classifier observation")
		}
		seen[key] = true
		for _, interval := range entry.Coverage.Segments {
			if durationMS > 0 && interval.EndSeconds > float64(durationMS)/1000+0.001 {
				return errors.New("librarypack: classifier coverage exceeds recording duration")
			}
		}
	}
	return nil
}

func (g *Generation) ClassifierEvidence(ctx context.Context, id string) ([]core.MusicClassifierEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil || g.db == nil {
		return nil, errors.New("librarypack: generation is closed")
	}
	if !g.hasClassifierEvidence {
		return nil, nil
	}
	var raw []byte
	if err := g.db.QueryRowContext(ctx, "SELECT data FROM classifier_evidence WHERE track_id=?", id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var evidence []core.MusicClassifierEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, err
	}
	return evidence, ctx.Err()
}

// ClassifierTrackIDs reports availability without loading model scores or
// vectors. It is used by offline coverage inventory, not foreground ranking.
func (g *Generation) ClassifierTrackIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil || g.db == nil {
		return nil, errors.New("librarypack: generation is closed")
	}
	if !g.hasClassifierEvidence {
		return nil, nil
	}
	rows, err := g.db.QueryContext(ctx, "SELECT track_id FROM classifier_evidence ORDER BY track_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (g *Generation) validateClassifierEvidenceRows(ctx context.Context, limits Limits) error {
	var exists int
	if err := g.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='classifier_evidence'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if g.manifest.Coverage.Classifier != 0 {
			return errors.New("librarypack: classifier coverage without evidence table")
		}
		return nil
	}
	var count, distinct, maximum, dangling int
	if err := g.db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT e.track_id),COALESCE(MAX(length(e.data)),0),COALESCE(SUM(CASE WHEN t.id IS NULL THEN 1 ELSE 0 END),0) FROM classifier_evidence e LEFT JOIN tracks t ON t.id=e.track_id`).Scan(&count, &distinct, &maximum, &dangling); err != nil {
		return err
	}
	if count != distinct || count != g.manifest.Coverage.Classifier || count > g.manifest.Coverage.Tracks || maximum > limits.MaxJSONBytes || dangling != 0 {
		return errors.New("librarypack: invalid classifier evidence rows or allocation limits")
	}
	rows, err := g.db.QueryContext(ctx, "SELECT track_id,data FROM classifier_evidence ORDER BY track_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		track, ok, err := g.Lookup(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("librarypack: classifier track missing")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&track.ClassifierEvidence); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("librarypack: trailing classifier evidence JSON")
		}
		clapBytes := 0
		if g.manifest.Version >= FormatVersion {
			if err := g.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT length(data) FROM clap_evidence WHERE track_id=?),0)", id).Scan(&clapBytes); err != nil {
				return err
			}
		}
		if len(raw)+clapBytes+classifierMetadataBytes(track) > limits.MaxRecordBytes {
			return errors.New("librarypack: combined classifier evidence allocation limit exceeded")
		}
		if err := validateClassifierEvidence(track.ClassifierEvidence, track.DurationMilliseconds); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	g.hasClassifierEvidence = true
	return nil
}
