package multichannel

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// automaticBatch has no backing catalog or provider. Once preparation returns,
// ranking, selection, duration fitting and sequencing can only read this batch.
type automaticBatch struct {
	calibrations []core.AudioSimilarityCalibration
	ids          []string
	rows         map[string]int
	meta         map[string]core.TrackMeta
	vectors      map[string]ports.Vectors
	mert         map[string]core.LibraryVector
	clap         map[string]core.LibraryVector
	recordings   map[string]core.EnrichedTrack
	features     map[string]core.TrackFeatures
	assessments  map[string]core.AudioAssessment
	familiarity  map[string]float64
}

func newAutomaticBatch() *automaticBatch {
	return &automaticBatch{rows: map[string]int{}, meta: map[string]core.TrackMeta{}, vectors: map[string]ports.Vectors{}, mert: map[string]core.LibraryVector{}, clap: map[string]core.LibraryVector{}, recordings: map[string]core.EnrichedTrack{}, features: map[string]core.TrackFeatures{}, assessments: map[string]core.AudioAssessment{}, familiarity: map[string]float64{}}
}

// All nested maps/slices entering the frozen batch are copied. They may have
// originated in a shared cache, or in a caller-owned saved knowledge snapshot.
func automaticCopy[T any](value T) T {
	bytes, err := json.Marshal(value)
	if err != nil {
		return *new(T)
	}
	var result T
	if json.Unmarshal(bytes, &result) != nil {
		return *new(T)
	}
	return result
}

func (a *AutomaticEngine) prepareBatch(ctx context.Context, cat ports.Catalog, tracks []core.TrackRef, intent core.MusicIntent) (*automaticBatch, error) {
	b := newAutomaticBatch()
	b.calibrations = automaticCopy(a.calibrations)
	knowledge := map[string]core.EnrichedTrack{}
	if intent.Knowledge != nil {
		for _, t := range intent.Knowledge.Tracks {
			knowledge[t.Ref.ID] = t
		}
	}
	for _, ref := range tracks {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		if _, seen := b.rows[ref.ID]; seen || ref.ID == "" {
			continue
		}
		meta, ok := ports.CatalogMeta(ctx, cat, ref.ID)
		if !ok {
			if ctx.Err() != nil {
				return b, ctx.Err()
			}
			continue
		}
		// Stage a complete recording atomically. Canceled reads cannot create a
		// metadata-only tail which differs depending on cancellation timing.
		var vector ports.Vectors
		if v, ok := cat.Vectors(ref.ID); ok {
			vector = ports.Vectors{Audio: append([]float32(nil), v.Audio...), Track: append([]float32(nil), v.Track...)}
		}
		var mert, clap core.LibraryVector
		if source, ok := cat.(ports.LibraryAudioCatalog); ok {
			v, found, err := source.LibraryVector(ctx, ref.ID)
			if err == nil && found {
				mert = automaticCopy(v)
			}
		}
		var assessment core.AudioAssessment
		if source, ok := cat.(ports.LibrarySemanticCatalog); ok {
			if v, found, err := source.LibraryCLAPVector(ctx, ref.ID); err == nil && found {
				clap = automaticCopy(v)
			}
			if v, found, err := source.LibraryAssessment(ctx, ref.ID); err == nil && found && v.TrackID == ref.ID {
				assessment = automaticCopy(v)
			}
		}
		recording := knowledge[ref.ID]
		if source, ok := cat.(ports.LibraryMetadataCatalog); ok {
			if v, found, err := source.LibraryRecordingMetadata(ctx, ref.ID); err == nil && found {
				if recording.Ref.ID == "" {
					recording = v
				} else {
					recording = mergeRecordingMetadata(recording, v)
				}
			}
		}
		if recording.Ref.ID == "" {
			recording = core.EnrichedTrack{Ref: meta.Ref, RecordingID: meta.MusicBrainzRecording, ISRC: meta.ISRC, Matched: true, IdentityStatus: core.ResolutionResolved}
			if duration := meta.FullRecordingDuration; duration.Valid() && !strings.Contains(strings.ToLower(duration.Source), "preview") {
				durationID := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(duration.RecordingID)), "musicbrainz:")
				for _, id := range []string{meta.Ref.ID, meta.Ref.RecordingIdentity, meta.MusicBrainzRecording} {
					if id != "" && durationID == strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "musicbrainz:") {
						copied := *duration
						recording.FullRecordingDuration = &copied
						break
					}
				}
			}
		}
		if recording.Ref.ID != ref.ID || meta.MusicBrainzRecording != "" && recording.RecordingID != "" && !strings.EqualFold(meta.MusicBrainzRecording, recording.RecordingID) || recordingISRCConflict(recording, meta.ISRC) {
			recording = core.EnrichedTrack{Ref: meta.Ref, IdentityStatus: core.ResolutionAmbiguous}
		}
		var features core.TrackFeatures
		if a.features != nil {
			if v, found, err := a.features.Features(ctx, ref.ID); err == nil && found && v.TrackID == ref.ID {
				features = automaticCopy(v)
			}
		}
		popularity, popular := 0.0, false
		if a.familiarity != nil {
			popularity, popular = a.familiarity(ctx, meta.Ref)
		}
		if err := ctx.Err(); err != nil {
			return b, err
		}
		b.rows[ref.ID] = len(b.ids)
		b.ids = append(b.ids, ref.ID)
		b.meta[ref.ID] = automaticCopy(meta)
		b.vectors[ref.ID] = vector
		if len(mert.Values) > 0 {
			b.mert[ref.ID] = mert
		}
		if len(clap.Values) > 0 {
			b.clap[ref.ID] = clap
		}
		b.recordings[ref.ID] = automaticCopy(recording)
		b.features[ref.ID] = features
		if assessment.TrackID != "" {
			b.assessments[ref.ID] = assessment
		}
		if popular && finite(popularity) && popularity >= 0 && popularity <= 1 {
			b.familiarity[ref.ID] = popularity
		}
	}
	return b, nil
}
func (b *automaticBatch) Len() int { return len(b.ids) }
func (b *automaticBatch) Dim() int {
	for _, v := range b.vectors {
		if len(v.Audio) > 0 {
			return len(v.Audio)
		}
	}
	return 0
}
func (b *automaticBatch) ID(i int) string {
	if i < 0 || i >= len(b.ids) {
		return ""
	}
	return b.ids[i]
}
func (b *automaticBatch) RowOf(id string) (int, bool)           { i, ok := b.rows[id]; return i, ok }
func (b *automaticBatch) Meta(id string) (core.TrackMeta, bool) { v, ok := b.meta[id]; return v, ok }
func (b *automaticBatch) Vectors(id string) (ports.Vectors, bool) {
	v, ok := b.vectors[id]
	return v, ok && (len(v.Audio) > 0 || len(v.Track) > 0)
}
func (b *automaticBatch) VectorsByRow(i int) (ports.Vectors, bool) { return b.Vectors(b.ID(i)) }
func (*automaticBatch) RawRow(int) ([]int8, []int8, bool)          { return nil, nil, false }
func (b *automaticBatch) Resolve(query string, limit int) []core.TrackRef {
	var out []core.TrackRef
	for _, id := range b.ids {
		t := b.meta[id].Ref
		if strings.Contains(strings.ToLower(t.Display()), strings.ToLower(query)) {
			out = append(out, t)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
func (b *automaticBatch) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	v, ok := b.mert[id]
	return v, ok, ctx.Err()
}
func (*automaticBatch) LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool) {
	return 0, false
}
func (*automaticBatch) BindLibraryQueries(core.AudioModelIdentity, []core.AudioClauseVector) {}
func (b *automaticBatch) LibraryAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	v, ok := b.assessments[id]
	return v, ok, ctx.Err()
}
func (b *automaticBatch) LibraryCLAPVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	v, ok := b.clap[id]
	return v, ok, ctx.Err()
}
func (b *automaticBatch) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	v, ok := b.recordings[id]
	return v, ok, ctx.Err()
}
func (*automaticBatch) LibraryTrackFeatures(context.Context, string) (core.LibraryTrackFeatures, bool) {
	return core.LibraryTrackFeatures{}, false
}
func (*automaticBatch) LibraryPreferenceScore(context.Context, string, core.MusicIntent, string) (float64, bool) {
	return 0, false
}
