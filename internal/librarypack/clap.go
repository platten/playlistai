package librarypack

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

const CLAPEvidenceScope = "recording-relative-excerpts"
const maxCLAPSegments = 128

// CLAPSegment describes observed recording audio separately from encoder input.
// Repetition/padding never contributes to ObservedSeconds or CoveredSeconds.
// Invalid/unavailable segments have no vector and retain their failure reason.
type CLAPSegment struct {
	Index           int       `json:"index"`
	StartSeconds    float64   `json:"startSeconds"`
	EndSeconds      float64   `json:"endSeconds"`
	ObservedSeconds float64   `json:"observedSeconds"`
	InputSeconds    float64   `json:"inputSeconds,omitempty"`
	Padding         string    `json:"padding"`  // none | repeat | zero | unknown
	Validity        string    `json:"validity"` // valid | invalid | unavailable
	Reason          string    `json:"reason,omitempty"`
	Vector          []float32 `json:"vector,omitempty"`
}

// CLAPEvidence is optional v7 evidence. Pooled-only v5/v6 data never acquires
// synthetic segments or measured coverage on import or merge.
type CLAPEvidence struct {
	Model          *core.AudioModelIdentity `json:"model,omitempty"`
	Sampling       string                   `json:"sampling"`
	Scope          string                   `json:"scope"`
	CoveredSeconds float64                  `json:"coveredSeconds"`
	Incomplete     bool                     `json:"incomplete,omitempty"`
	PartialReason  string                   `json:"partialReason,omitempty"`
	Segments       []CLAPSegment            `json:"segments"`
}

func validCLAPModel(model core.AudioModelIdentity, space VectorSpace) bool {
	return model.Model == space.Model && model.Revision == space.ModelRevision &&
		model.Preprocessing == space.Preprocessing && model.Dimension == space.Dimension &&
		model.Weights == space.GraphSHA256 && validHash(model.Weights) && strings.TrimSpace(model.Runtime) != ""
}

// CLAPCompatible requires the paired audio/text/tokenizer fingerprint, not just
// a model name or dimension. The v6 indexer stored that fingerprint in
// GraphSHA256; v7 additionally preserves the runtime as part of the identity.
// V7 packs without an explicit identity remain unpaired until a producer can
// establish it; an absent identity is never inferred from sampled coverage.
func (m Manifest) CLAPCompatible(model core.AudioModelIdentity) bool {
	if m.Coverage.CLAP == 0 || !validCLAPModel(model, m.CLAP) {
		return false
	}
	if m.CLAPModel != nil {
		return *m.CLAPModel == model
	}
	return m.Version == PooledCLAPVersion
}

func validateCLAPEvidence(e *CLAPEvidence, dim int, pooled []float32, durationMS int64) error {
	if e == nil {
		return nil
	}
	if len(pooled) != dim || dim <= 0 || len(e.Segments) == 0 || len(e.Segments) > maxCLAPSegments ||
		strings.TrimSpace(e.Sampling) == "" || e.Scope != CLAPEvidenceScope ||
		!finitePositive(e.CoveredSeconds) || e.Incomplete != (strings.TrimSpace(e.PartialReason) != "") {
		return errors.New("librarypack: invalid CLAP evidence contract")
	}
	var covered, previousEnd float64
	previousIndex := -1
	sum := make([]float64, dim)
	for _, segment := range e.Segments {
		if segment.Index <= previousIndex || !finiteNonnegative(segment.StartSeconds) || !finiteNonnegative(segment.EndSeconds) ||
			!finiteNonnegative(segment.ObservedSeconds) || !finiteNonnegative(segment.InputSeconds) || segment.EndSeconds < segment.StartSeconds ||
			segment.StartSeconds < previousEnd-1e-6 || math.Abs(segment.EndSeconds-segment.StartSeconds-segment.ObservedSeconds) > 1e-5 ||
			durationMS > 0 && segment.EndSeconds > float64(durationMS)/1000+.002 {
			return errors.New("librarypack: invalid CLAP segment coverage")
		}
		previousIndex, previousEnd = segment.Index, segment.EndSeconds
		switch segment.Padding {
		case "none":
			if math.Abs(segment.InputSeconds-segment.ObservedSeconds) > 1e-5 {
				return errors.New("librarypack: unpadded CLAP duration mismatch")
			}
		case "repeat", "zero":
			if segment.InputSeconds <= segment.ObservedSeconds {
				return errors.New("librarypack: invalid padded CLAP duration")
			}
		case "unknown":
			if segment.InputSeconds != 0 {
				return errors.New("librarypack: unknown CLAP padding has an asserted input duration")
			}
		default:
			return errors.New("librarypack: invalid CLAP padding state")
		}
		switch segment.Validity {
		case "valid":
			if !finitePositive(segment.ObservedSeconds) || len(segment.Vector) != dim || !validUnitVector(segment.Vector) || segment.Reason != "" {
				return errors.New("librarypack: invalid CLAP segment vector")
			}
			covered += segment.ObservedSeconds
			for i, value := range segment.Vector {
				sum[i] += float64(value) * segment.ObservedSeconds
			}
		case "invalid", "unavailable":
			if len(segment.Vector) != 0 || strings.TrimSpace(segment.Reason) == "" || !e.Incomplete {
				return errors.New("librarypack: invalid CLAP missing segment state")
			}
		default:
			return errors.New("librarypack: unknown CLAP segment validity")
		}
	}
	if math.Abs(covered-e.CoveredSeconds) > 1e-5 {
		return errors.New("librarypack: CLAP coverage differs from valid observed segments")
	}
	var norm float64
	for _, value := range sum {
		norm += value * value
	}
	if norm <= 1e-12 {
		return errors.New("librarypack: degenerate CLAP segment aggregate")
	}
	for i, value := range sum {
		if math.Abs(value/math.Sqrt(norm)-float64(pooled[i])) > 1e-4 {
			return errors.New("librarypack: pooled CLAP differs from its segment evidence")
		}
	}
	return nil
}

func finiteNonnegative(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func finitePositive(v float64) bool    { return v > 0 && finiteNonnegative(v) }

func clapEvidenceJSON(track Track, limits Limits) ([]byte, error) {
	if track.CLAPEvidence == nil {
		return nil, nil
	}
	if err := validateCLAPEvidence(track.CLAPEvidence, len(track.CLAP), track.CLAP, track.DurationMilliseconds); err != nil {
		return nil, fmt.Errorf("track %s: %w", track.ID, err)
	}
	raw, err := json.Marshal(track.CLAPEvidence)
	if err != nil {
		return nil, err
	}
	if len(raw) > limits.MaxJSONBytes || len(raw)+len(track.RawTags)+len(track.DSP)+len(track.Missingness) > limits.MaxRecordBytes {
		return nil, errors.New("librarypack: CLAP evidence allocation limit exceeded")
	}
	metadataBytes := len(track.ID) + len(track.Artist) + len(track.Title) + len(track.NormalizedArtist) + len(track.NormalizedTitle) +
		len(track.SourceIdentity) + len(track.RecordingIdentity) + len(track.ISRC) + len(track.MusicBrainzRecording) + len(track.AcoustID) +
		len(track.DurationProvenance) + len(track.AlbumArtist) + len(track.Album) + len(track.RootAlias) + len(track.RelativePath) +
		len(track.Failure) + len(track.Unsupported) + len(track.RawTags) + len(track.DSP) + len(track.Missingness)
	capabilities, _ := json.Marshal(track.Capabilities)
	metadataBytes += len(capabilities)
	if fingerprint := track.AudioFingerprint; fingerprint != nil {
		metadataBytes += len(fingerprint.Contract) + len(fingerprint.Format) + len(fingerprint.Fingerprint) + len(fingerprint.FingerprintSHA256) + len(fingerprint.Scope) + len(fingerprint.DecoderRuntimeID)
	}
	if metadataBytes+len(raw) > limits.MaxRecordBytes {
		return nil, errors.New("librarypack: combined metadata and CLAP evidence allocation limit exceeded")
	}
	return raw, nil
}

// CLAPEvidence returns an owned, validated segment record, or unavailable for
// tracks without optional rich evidence and all legacy v5/v6 generations.
func (g *Generation) CLAPEvidence(ctx context.Context, id string) (*CLAPEvidence, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if g == nil || g.db == nil {
		return nil, false, errors.New("librarypack: generation is closed")
	}
	if g.manifest.Version < FormatVersion {
		return nil, false, nil
	}
	var raw []byte
	if err := g.db.QueryRowContext(ctx, "SELECT data FROM clap_evidence WHERE track_id=?", id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	var evidence CLAPEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, false, err
	}
	return &evidence, true, nil
}

func (g *Generation) validateCLAPEvidenceRows(ctx context.Context, limits Limits) error {
	if g.manifest.Version < FormatVersion {
		return nil
	}
	var count, distinct, maximum, maxCombined, dangling int
	if err := g.db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT e.track_id),COALESCE(MAX(length(e.data)),0),
		COALESCE(MAX(length(e.data)+length(CAST(t.id||t.artist||t.title||t.normalized_artist||t.normalized_title||t.source_identity||t.recording_identity||t.isrc||t.musicbrainz_recording||t.acoustid||t.duration_provenance||t.album_artist||t.album||t.root_alias||t.relative_path||t.fingerprint_contract||t.fingerprint_format||t.fingerprint_value||t.fingerprint_sha256||t.fingerprint_scope||t.fingerprint_decoder||t.capabilities_json||t.raw_tags_json||t.dsp_json||t.missingness_json||t.failure||t.unsupported AS BLOB))),0),
		COALESCE(SUM(CASE WHEN t.id IS NULL OR t.clap_row IS NULL THEN 1 ELSE 0 END),0) FROM clap_evidence e LEFT JOIN tracks t ON t.id=e.track_id`).Scan(&count, &distinct, &maximum, &maxCombined, &dangling); err != nil {
		return err
	}
	if count != distinct || count > g.manifest.Coverage.CLAP || maximum > limits.MaxJSONBytes || maxCombined > limits.MaxRecordBytes || dangling != 0 {
		return errors.New("librarypack: invalid CLAP evidence rows or allocation limits")
	}
	rows, err := g.db.QueryContext(ctx, `SELECT e.data,t.clap_row,t.duration_ms FROM clap_evidence e JOIN tracks t ON t.id=e.track_id ORDER BY e.track_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw []byte
		var vectorRow, duration int64
		if err := rows.Scan(&raw, &vectorRow, &duration); err != nil {
			return err
		}
		var evidence CLAPEvidence
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&evidence); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return errors.New("librarypack: trailing CLAP evidence JSON")
		}
		pooled, _, err := g.clapVectorAt(ctx, vectorRow)
		if err != nil {
			return err
		}
		if evidence.Sampling != g.manifest.CLAP.Sampling {
			return errors.New("librarypack: CLAP sampling differs from manifest")
		}
		if evidence.Model != nil && (!validCLAPModel(*evidence.Model, g.manifest.CLAP) || g.manifest.CLAPModel != nil && *evidence.Model != *g.manifest.CLAPModel) {
			return errors.New("librarypack: CLAP evidence model differs from manifest")
		}
		if err := validateCLAPEvidence(&evidence, g.manifest.CLAP.Dimension, pooled, duration); err != nil {
			return err
		}
	}
	return rows.Err()
}
