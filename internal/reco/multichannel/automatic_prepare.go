package multichannel

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Baseline local evidence is complete before optional network work starts. A
// timeout keeps that batch and every completed addition; assembly never calls a
// provider. The parent's work deadline includes the assembly reserve.
func (a *AutomaticEngine) acquireAutomaticEvidence(ctx context.Context, cat ports.Catalog, b *automaticBatch, intent core.MusicIntent, catalogVersion string, metadataOrder []string) error {
	ctx = ports.WithAudioMetadataCatalog(ctx, cat)
	applyVerification := func(id string, updated core.EnrichedTrack, err error) {
		meta, recording := b.meta[id], b.recordings[id]
		if updated.Ref.ID != id || !strings.EqualFold(updated.RecordingID, recording.RecordingID) || err != nil && len(updated.Claims) == 0 {
			return
		}
		if updated.IdentityStatus == core.ResolutionAmbiguous || updated.Matched && updated.IdentityStatus == core.ResolutionResolved && recordingISRCConflict(updated, meta.ISRC) {
			updated.IdentityStatus, updated.Matched, updated.Claims = core.ResolutionAmbiguous, false, nil
			b.recordings[id] = automaticCopy(updated)
		} else if updated.Matched && updated.IdentityStatus == core.ResolutionResolved {
			b.recordings[id] = automaticCopy(updated)
		}
	}
	// Cached conflicts are local evidence, not optional online opportunities.
	// Check every prepared row before spending time on inference or providers.
	if verifier, ok := a.enricher.(ports.CachedRecordingVerifier); ok {
		for _, id := range b.ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			recording := b.recordings[id]
			if recording.RecordingID == "" || recording.IdentityStatus != core.ResolutionResolved {
				continue
			}
			updated, err := verifier.VerifyCachedRecording(ctx, automaticCopy(recording))
			applyVerification(id, updated, err)
		}
	}
	var service *audio.Service
	var session *audio.Session
	if a.audioProvider != nil && catalogVersion != "" {
		service = a.audioProvider()
		if service != nil && service.InferenceReady() {
			analysisIntent := intent
			analysisIntent.VerificationPolicy = core.BestAvailable
			var err error
			session, err = service.Begin(ctx, analysisIntent, catalogVersion, nil)
			if err != nil {
				return err
			}
			defer session.Close()
		}
	}
	// Spend local-cache opportunities before the first provider request. A
	// slow missing preview cannot hide a later reusable analysis on stop.
	order := b.ids
	cached := map[string]bool{}
	if session != nil {
		var hits, missing []string
		for _, id := range b.ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			record, found, err := service.Store.Find(ctx, catalogVersion, id, core.ProvisionalRecordingKey(b.meta[id].Ref), service.Analyzer.Identity())
			if err == nil && found && record.Identity.CurrentPolicy() {
				cached[id] = true
				hits = append(hits, id)
			} else {
				missing = append(missing, id)
			}
		}
		hits = append(hits, missing...)
		order = hits
	}
	checkPreview := func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		meta := b.meta[id]
		if session == nil || b.recordings[id].IdentityStatus == core.ResolutionAmbiguous {
			return nil
		}
		if prior := b.assessments[id]; prior.AnalysisID != "" {
			return nil
		}
		assessment, err := session.Check(ctx, meta.Ref, false)
		if assessment.AnalysisID != "" && assessment.Identity.CurrentPolicy() && assessment.Identity.Status == core.ResolutionResolved {
			b.assessments[id] = automaticCopy(assessment)
			// Session.Check already populated or reused this exact model's cache. This
			// second read is local and never downloads a preview again.
			record, found, readErr := service.Store.Find(ctx, catalogVersion, id, core.ProvisionalRecordingKey(meta.Ref), service.Analyzer.Identity())
			// A preview uses its own pooling space. It can fill missing audio,
			// but must not replace the prepared vector used by seed comparisons.
			if readErr == nil && found && record.ID == assessment.AnalysisID && len(b.clap[id].Values) == 0 {
				if vector := automaticPreviewVector(record); len(vector.Values) > 0 {
					b.clap[id] = vector
				}
			}
		}
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	// Materialize reusable preview evidence before the first metadata request.
	for _, id := range order {
		if cached[id] {
			if err := checkPreview(id); err != nil {
				return err
			}
		}
	}
	enriched := 0
	var criteria []core.MusicalCriterion
	for _, clause := range audio.Clauses(intent) {
		criteria = append(criteria, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text, Scope: clause.Scope, Strength: clause.Strength, Group: clause.Group, CoverageGroup: clause.CoverageGroup})
	}
	if len(metadataOrder) == 0 {
		metadataOrder = b.ids
	}
	for _, id := range metadataOrder {
		if err := ctx.Err(); err != nil {
			return err
		}
		meta, recording := b.meta[id], b.recordings[id]
		// Catalog membership identifies the local row. An embedded external
		// recording ID can still contradict independently fetched metadata.
		library := strings.HasPrefix(id, "pack:") || strings.HasPrefix(id, "local:")
		if verifier, ok := a.enricher.(ports.RecordingVerifier); ok && library && enriched < 32 && recording.RecordingID != "" && recording.IdentityStatus == core.ResolutionResolved && recording.FullRecordingDuration.Valid() {
			enriched++
			lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
			updated, err := verifier.VerifyRecording(lookup, automaticCopy(recording), criteria)
			cancel()
			// Preserve a demonstrated conflict, never substitute a different
			// recording. A provider failure alone is still missing evidence.
			applyVerification(id, updated, err)
		}
		if !cached[id] && !library && a.enricher != nil && recording.RecordingID == "" && enriched < 32 {
			enriched++
			lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
			rows, _ := a.enricher.Enrich(lookup, []core.TrackRef{meta.Ref}, nil)
			cancel()
			if len(rows) == 1 && rows[0].Ref.ID == id && rows[0].Matched && rows[0].IdentityStatus == core.ResolutionResolved {
				row := rows[0]
				if meta.MusicBrainzRecording != "" && !strings.EqualFold(meta.MusicBrainzRecording, row.RecordingID) || recordingISRCConflict(row, meta.ISRC) {
					row = core.EnrichedTrack{Ref: meta.Ref, IdentityStatus: core.ResolutionAmbiguous}
				}
				b.recordings[id] = automaticCopy(row)
			}
		}
	}
	// Fetch missing previews only after ranked, bounded metadata checks.
	for _, id := range order {
		if !cached[id] {
			if err := checkPreview(id); err != nil {
				return err
			}
		}
	}

	return ctx.Err()
}

func automaticPreviewVector(record core.AudioAnalysis) core.LibraryVector {
	dimension := record.Model.Dimension
	if dimension <= 0 || len(record.Segments) == 0 {
		return core.LibraryVector{}
	}
	values := make([]float32, dimension)
	for _, segment := range record.Segments {
		if len(segment.Embedding) != dimension {
			return core.LibraryVector{}
		}
		for i, value := range segment.Embedding {
			if !finite(float64(value)) {
				return core.LibraryVector{}
			}
			values[i] += value / float32(len(record.Segments))
		}
	}
	return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: audio.Fingerprint(struct {
		Model   core.AudioModelIdentity
		Pooling string
	}{record.Model, "preview-segment-mean/v1"}), Scope: "sampled_audio"}, Values: values}
}

// Identity checks prioritize likely outputs without running musical ranking or assembly.
func (a *AutomaticEngine) automaticMetadataOrder(b *automaticBatch, candidates []core.Candidate, required []core.TrackRef, seed int64) []string {
	order := make([]string, 0, len(b.ids))
	seen := map[string]bool{}
	add := func(id string) {
		if _, exists := b.meta[id]; exists && !seen[id] {
			order = append(order, id)
			seen[id] = true
		}
	}
	for _, ref := range required {
		add(ref.ID)
	}
	ranked := append([]core.Candidate(nil), candidates...)
	score := func(c core.Candidate) float64 {
		sum := 0.0
		for _, source := range c.Sources {
			sum += 1 / (a.cfg.ReciprocalRankConstant + float64(max(1, source.Rank)))
		}
		return sum
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := score(ranked[i]), score(ranked[j])
		if left != right {
			return left > right
		}
		lh, rh := stableHash(ranked[i].Track.ID, seed), stableHash(ranked[j].Track.ID, seed)
		if lh != rh {
			return lh < rh
		}
		return ranked[i].Track.ID < ranked[j].Track.ID
	})
	for _, candidate := range ranked {
		add(candidate.Track.ID)
	}
	for _, id := range b.ids {
		add(id)
	}
	return order
}
