package multichannel

import (
	"context"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

func (o *Orchestrator) recordingDuration(id string) (*core.RecordingDuration, bool) {
	// Snapshot evidence is recording-specific, unlike preview analysis length.
	if track, ok := o.knowledgeTrack(id); ok && track.Matched && track.IdentityStatus == core.ResolutionResolved &&
		track.FullRecordingDuration.Valid() && track.RecordingID == track.FullRecordingDuration.RecordingID {
		duration := *track.FullRecordingDuration
		return &duration, true
	}
	if meta, ok := o.cat.Meta(id); ok && meta.FullRecordingDuration.Valid() {
		duration := *meta.FullRecordingDuration
		return &duration, true
	}
	return nil, false
}

func (o *Orchestrator) assessDuration(tracks []core.TrackRef, intent core.MusicIntent) *core.PlaylistDurationAssessment {
	if intent.DurationSeconds <= 0 {
		return nil
	}
	out := &core.PlaylistDurationAssessment{TargetSeconds: intent.DurationSeconds, ToleranceSeconds: intent.DurationTolerance(), State: core.EvidenceUnknown}
	for _, track := range tracks {
		if duration, ok := o.recordingDuration(track.ID); ok {
			out.KnownMilliseconds += duration.Milliseconds
			out.Evidence = append(out.Evidence, core.TrackDurationEvidence{TrackID: track.ID, RecordingDuration: *duration})
		} else {
			out.UnknownTrackIDs = append(out.UnknownTrackIDs, track.ID)
		}
	}
	if len(out.UnknownTrackIDs) == 0 {
		out.State = core.EvidenceMismatch
		if len(tracks) > 0 && durationDistance(out.KnownMilliseconds, intent) == 0 {
			out.State = core.EvidenceMatch
		}
	}
	return out
}

func durationDistance(milliseconds int64, intent core.MusicIntent) int64 {
	delta := milliseconds - int64(intent.DurationSeconds)*1000
	if delta < 0 {
		delta = -delta
	}
	return max(int64(0), delta-int64(intent.DurationTolerance())*1000)
}

func (o *Orchestrator) durationReadyToCheck(candidates []core.Candidate, required []core.TrackRef, intent core.MusicIntent) bool {
	if intent.DurationSeconds <= 0 || intent.HasExplicitTrackCount() {
		return false
	}
	var total int64
	for _, track := range required {
		if d, ok := o.recordingDuration(track.ID); ok {
			total += d.Milliseconds
		} else {
			return false
		}
	}
	for _, candidate := range candidates {
		if d, ok := o.recordingDuration(candidate.Track.ID); ok {
			total += d.Milliseconds
		}
	}
	return total >= int64(max(0, intent.DurationSeconds-intent.DurationTolerance()))*1000
}

type durationObjective struct {
	unknown  int
	distance int64
}

func (d durationObjective) better(other durationObjective) bool {
	return d.unknown < other.unknown || d.unknown == other.unknown && d.distance < other.distance
}

// fitDuration searches only the already eligible, relevance-screened pool. It
// preserves fixed recordings and (when explicit) count. Variable-count requests
// use at most MaxCount tracks. Deterministic single additions/removals/swaps are
// bounded; a local minimum is reported as partial, never as proof of infeasibility.
// Sequencing is checked again before the caller accepts the proposed selection.
func (o *Orchestrator) fitDuration(ctx context.Context, intent core.MusicIntent, selected, pool []core.Candidate, required []core.TrackRef, reserved int) ([]core.Candidate, error) {
	out := append([]core.Candidate(nil), selected...)
	known := map[string]int64{}
	for _, candidate := range append(append([]core.Candidate(nil), pool...), selected...) {
		if d, ok := o.recordingDuration(candidate.Track.ID); ok {
			known[candidate.Track.ID] = d.Milliseconds
		}
	}
	var fixedMilliseconds int64
	for _, track := range required {
		d, ok := o.recordingDuration(track.ID)
		if !ok {
			return out, nil // no subset can verify an unknown mandatory recording
		}
		fixedMilliseconds += d.Milliseconds
	}
	startedUnknown := false
	for _, candidate := range out {
		startedUnknown = startedUnknown || known[candidate.Track.ID] == 0
	}
	// Removing unknown tracks cannot establish a useful duration fit when the
	// entire known pool is too short. Preserve the ordinary musical result.
	available := fixedMilliseconds
	for _, ms := range known {
		available += ms
	}
	if startedUnknown && available < int64(max(0, intent.DurationSeconds-intent.DurationTolerance()))*1000 {
		return out, nil
	}
	variable := !intent.HasExplicitTrackCount()
	for pass := 0; pass < 2*core.MaxCount; pass++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		total, unknown := fixedMilliseconds, 0
		ids, recordings := map[string]bool{}, map[string]bool{}
		for _, track := range required {
			ids[track.ID], recordings[core.ProvisionalRecordingKey(track)] = true, true
		}
		for _, candidate := range out {
			ids[candidate.Track.ID], recordings[core.ProvisionalRecordingKey(candidate.Track)] = true, true
			if ms := known[candidate.Track.ID]; ms > 0 {
				total += ms
			} else {
				unknown++
			}
		}
		current := durationObjective{unknown, durationDistance(total, intent)}
		if current.unknown == 0 && current.distance == 0 {
			break
		}
		best := current
		remove, add := -1, -1
		consider := func(drop, insert int, ms int64, missing int) {
			next := durationObjective{missing, durationDistance(ms, intent)}
			if next.better(best) {
				best, remove, add = next, drop, insert
			}
		}
		// Iterate weaker/later choices first for equally useful removals. Pool
		// insertion ties retain the selector's relevance/diversity ordering.
		for i := len(out) - 1; i >= reserved; i-- {
			missing := unknown
			oldMS := known[out[i].Track.ID]
			if oldMS == 0 {
				missing--
			}
			if variable && len(out)+len(required) > max(1, reserved+len(required)) {
				consider(i, -1, total-oldMS, missing)
			}
			for j, candidate := range pool {
				if j%64 == 0 && ctx.Err() != nil {
					return nil, ctx.Err()
				}
				ms := known[candidate.Track.ID]
				if ms == 0 || ids[candidate.Track.ID] || recordings[core.ProvisionalRecordingKey(candidate.Track)] || durationFitDowngrade(out[i], candidate) {
					continue
				}
				consider(i, j, total-oldMS+ms, missing)
			}
		}
		if variable && len(out)+len(required) < core.MaxCount {
			for j, candidate := range pool {
				if j%64 == 0 && ctx.Err() != nil {
					return nil, ctx.Err()
				}
				ms := known[candidate.Track.ID]
				if ms > 0 && !ids[candidate.Track.ID] && !recordings[core.ProvisionalRecordingKey(candidate.Track)] {
					consider(-1, j, total+ms, unknown)
				}
			}
		}
		if !best.better(current) {
			break
		}
		if remove >= 0 && add >= 0 {
			out[remove] = pool[add]
		} else if remove >= 0 {
			out = append(out[:remove], out[remove+1:]...)
		} else {
			out = append(out, pool[add])
		}
	}
	if startedUnknown {
		// Missing metadata must not shrink a good musical result just to make
		// fewer unknown values appear in diagnostics. Accept that trade only
		// when the proposed full playlist actually satisfies the duration.
		tracks := append([]core.TrackRef(nil), required...)
		for _, candidate := range out {
			tracks = append(tracks, candidate.Track)
		}
		if o.assessDuration(tracks, intent).State != core.EvidenceMatch {
			return append([]core.Candidate(nil), selected...), nil
		}
	}
	return out, nil
}

func durationFitDowngrade(old, replacement core.Candidate) bool {
	return old.FitTier == fitStrong && replacement.FitTier != fitStrong ||
		old.MusicalFit == core.EvidenceMatch && replacement.MusicalFit != core.EvidenceMatch
}

func (o *Orchestrator) annotateDuration(playlist *core.Playlist) {
	assessment := o.assessDuration(playlist.Tracks, playlist.Intent)
	if assessment == nil {
		return
	}
	playlist.Duration = assessment
	if assessment.State == core.EvidenceMatch {
		playlist.Notices = append(playlist.Notices, core.PlaylistNotice{Code: "duration_matched", Detail: fmt.Sprintf("Full-recording metadata totals %.3f seconds, within %d seconds of the requested %d seconds.", float64(assessment.KnownMilliseconds)/1000, assessment.ToleranceSeconds, assessment.TargetSeconds)})
		return
	}
	code := "duration_target_unmet"
	detail := fmt.Sprintf("The selected full recordings total %.3f seconds; the requested target is %d seconds within %d seconds. The bounded candidate search did not find a fit while preserving the other requirements.", float64(assessment.KnownMilliseconds)/1000, assessment.TargetSeconds, assessment.ToleranceSeconds)
	if len(assessment.UnknownTrackIDs) > 0 {
		code = "duration_unsupported"
		detail = fmt.Sprintf("Full-recording durations are unavailable for %d selected track(s); the requested %d seconds within %d seconds cannot be verified. Preview lengths do not establish full-recording duration.", len(assessment.UnknownTrackIDs), assessment.TargetSeconds, assessment.ToleranceSeconds)
	}
	playlist.Notices = append(playlist.Notices, core.PlaylistNotice{Code: code, Detail: detail})
	playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: code, Detail: detail, Action: "Add verified full-recording metadata or adjust the duration or other requirements."})
	if playlist.Outcome.State == core.OutcomeFulfilled || playlist.Outcome.State == "" {
		playlist.Outcome.State = core.OutcomePartial
	}
}
