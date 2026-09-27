package multichannel

import (
	"context"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// candidateAssembly is the single final-selection operation used both to decide
// when checking can stop and to present the result. It does not fetch candidates
// or relax requirements. Context identities are canonicalized at this boundary.
type candidateAssembly struct {
	selection             ports.SelectionResult
	sequence              ports.SequenceResult
	stageMembership       []map[string]bool
	reserveReasons        []core.OutcomeReason
	countConflict         bool
	durationRequested     bool
	durationMatched       bool
	durationVariableCount bool
}

type completedAssembly struct {
	key    string
	result candidateAssembly
}

// Wiring/catalog objects are immutable for a generation. Everything that can
// change its result is fingerprinted by value, including mutable knowledge and
// request-local preview assessments. Serialization failure disables reuse.
func (o *Orchestrator) assemblyKey(candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) string {
	return o.assemblyKeyContext(context.Background(), candidates, intent, request, references, required, waypoints, seed)
}

func (o *Orchestrator) assemblyKeyContext(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) string {
	var assessments map[string]core.AudioAssessment
	if o.audioSession != nil {
		assessments = make(map[string]core.AudioAssessment, len(candidates)+len(required))
		for _, c := range append(candidatesForTracks(required), candidates...) {
			if a, exists := o.audioSession.Assessment(c.Track.ID); exists {
				assessments[c.Track.ID] = a
			}
		}
	}
	return audio.Fingerprint(struct {
		Candidates                              []core.Candidate
		Intent                                  core.MusicIntent
		Profile                                 core.TasteProfile
		Recent, References, Required, Waypoints []core.TrackRef
		Seed                                    int64
		Config                                  Config
		BestAvailable                           bool
		Knowledge                               *core.KnowledgeSnapshot
		Assessments                             map[string]core.AudioAssessment
		EnhancedFingerprint                     string
	}{candidates, intent, request.Profile, resolvedContextTracksContext(ctx, o.cat, request.RecentSelections), references, required, waypoints, seed, o.cfg, o.bestAvailable, o.knowledge, assessments, request.EnhancedAudio.Fingerprint() + o.enhancedSnapshot.Fingerprint() + EnhancedPolicyVersion + EnhancedDSPMappingVersion})
}

func (a candidateAssembly) complete(count int) bool {
	if a.countConflict || len(a.reserveReasons) > 0 || !a.durationVariableCount && len(a.sequence.Tracks) != count || a.durationRequested && !a.durationMatched {
		return false
	}
	for _, notice := range a.sequence.Notices {
		if notice.Code == "category_journey_exhausted" {
			return false
		}
	}
	return true
}

func (o *Orchestrator) assembleCandidates(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) (out candidateAssembly, outErr error) {
	parent := ctx
	if o.enhanced && o.requestContext != nil {
		if deadline, ok := o.requestContext.Deadline(); ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, deadline.Add(-time.Second))
			defer cancel()
		}
		defer func() {
			if outErr != nil && ctx.Err() != nil && parent.Err() == nil && o.bestAssembly != nil {
				out, outErr = *o.bestAssembly, nil
			}
			if outErr == nil && !out.countConflict && len(out.reserveReasons) == 0 && (o.bestAssembly == nil || len(out.sequence.Tracks) >= len(o.bestAssembly.sequence.Tracks)) {
				copy := out
				o.bestAssembly = &copy
			}
		}()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if reasons := o.requiredOutputReasons(ctx, required, intent); len(reasons) > 0 {
		out.reserveReasons = reasons
		return out, ctx.Err()
	}
	var err error
	candidates, err = o.filterConfirmedOutput(ctx, candidates, intent)
	if err != nil {
		return out, err
	}
	if err := o.prepareEnhanced(ctx, candidates, intent, request, references, required, waypoints); err != nil {
		return out, err
	}
	request.EnhancedAudio = o.enhancedSnapshot
	intent.Knowledge = o.recordingKnowledge(intent.Knowledge)
	key := ""
	if o.assemblyCache != nil {
		key = o.assemblyKeyContext(ctx, candidates, intent, request, references, required, waypoints, seed)
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if key != "" && o.assemblyCache.key == key {
			return o.assemblyCache.result, nil
		}
	}
	ranked, err := o.rankCandidates(ctx, append([]core.Candidate(nil), candidates...), ports.RankRequest{Intent: intent, Profile: request.Profile, EnhancedAudio: request.EnhancedAudio})
	if err != nil {
		return out, err
	}
	if o.search != nil {
		o.search.EligibleCandidates = append([]core.Candidate(nil), ranked...)
		for _, candidate := range ranked {
			o.rememberCandidateAssessment(candidate)
		}
	}
	reserved, remaining, reasons, err := o.reserveJourneyStages(ctx, ranked, required, intent)
	out.reserveReasons = reasons
	if err != nil {
		return out, err
	}
	variableDuration := intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount()
	if len(required)+len(reserved) > core.MaxCount || len(reasons) > 0 && !variableDuration && len(required)+len(reserved) >= intent.Count {
		out.countConflict = true
		return out, nil
	}
	fixed := append([]core.TrackRef(nil), required...)
	for _, candidate := range reserved {
		fixed = append(fixed, candidate.Track)
	}
	coverage, remaining, err := o.reservePlaylistCoverage(ctx, remaining, fixed, intent)
	if err != nil {
		return out, err
	}
	reserved = append(reserved, coverage...)
	for _, candidate := range coverage {
		fixed = append(fixed, candidate.Track)
	}
	selectionCount := intent.Count
	if variableDuration {
		selectionCount = max(selectionCount, len(fixed))
	}
	recent := resolvedContextTracksContext(ctx, o.cat, request.RecentSelections)
	out.selection, err = o.selector.Select(ctx, remaining, ports.SelectionRequest{
		Intent: intent, Required: fixed, Waypoints: waypoints,
		RecentSelections: recent, Count: selectionCount - len(fixed),
	})
	if err != nil {
		return out, err
	}
	out.selection.Candidates = append(reserved, out.selection.Candidates...)
	trajectoryWaypoints := waypoints
	if len(trajectoryWaypoints) < 2 && len(required) >= 2 {
		trajectoryWaypoints = required
	}
	var trajectory ports.Trajectory
	if (intent.Mode == core.ModeJourney || o.bestAvailable) && len(trajectoryWaypoints) >= 2 {
		trajectory = NewWaypointTrajectory(o.cat, trajectoryWaypoints)
	}
	out.stageMembership, err = o.categoryMembership(ctx, append(candidatesForTracks(required), out.selection.Candidates...), intent)
	if err != nil {
		return out, err
	}
	initialSequenceIntent := intent
	if variableDuration {
		initialSequenceIntent.Count, initialSequenceIntent.Controls.TotalTrackCount = selectionCount, selectionCount
	}
	out.sequence, err = o.sequencer.Sequence(ctx, ports.SequenceRequest{
		Intent: initialSequenceIntent, Candidates: out.selection.Candidates, Required: required, Waypoints: waypoints, EnhancedAudio: request.EnhancedAudio,
		ReferenceAnchors: references, RecentSelections: recent,
		Trajectory: trajectory, Seed: seed, CategoryStages: out.stageMembership,
	})
	if err == nil && intent.DurationSeconds > 0 {
		out.durationRequested, out.durationVariableCount = true, variableDuration
		assessment := o.assessDurationContext(ctx, out.sequence.Tracks, intent)
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if assessment.State != core.EvidenceMatch {
			// The normal selector establishes the same relevance floor and fit
			// policy for every alternative before duration can consider it.
			pool, poolErr := o.selector.Select(ctx, remaining, ports.SelectionRequest{Intent: intent, Required: fixed, Waypoints: waypoints, RecentSelections: recent, Count: len(remaining)})
			if poolErr != nil {
				return out, poolErr
			}
			proposed, fitErr := o.fitDuration(ctx, intent, out.selection.Candidates, pool.Candidates, required, len(reserved))
			if fitErr != nil {
				return out, fitErr
			}
			stages, stageErr := o.categoryMembership(ctx, append(candidatesForTracks(required), proposed...), intent)
			if stageErr != nil {
				return out, stageErr
			}
			sequenceIntent := intent
			if variableDuration {
				sequenceIntent.Count = len(proposed) + len(required)
				sequenceIntent.Controls.TotalTrackCount = sequenceIntent.Count
			}
			sequence, sequenceErr := o.sequencer.Sequence(ctx, ports.SequenceRequest{Intent: sequenceIntent, Candidates: proposed, Required: required, Waypoints: waypoints, EnhancedAudio: request.EnhancedAudio,
				ReferenceAnchors: references, RecentSelections: recent, Trajectory: trajectory, Seed: seed, CategoryStages: stages})
			if sequenceErr == nil && len(sequence.Tracks) == len(proposed)+len(required) {
				next := o.assessDurationContext(ctx, sequence.Tracks, intent)
				before := durationObjective{len(assessment.UnknownTrackIDs), durationDistance(assessment.KnownMilliseconds, intent)}
				after := durationObjective{len(next.UnknownTrackIDs), durationDistance(next.KnownMilliseconds, intent)}
				if after.better(before) {
					out.selection.Candidates, out.sequence, out.stageMembership = proposed, sequence, stages
					assessment = next
				}
			}
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
		}
		out.durationMatched = assessment.State == core.EvidenceMatch
	}
	if err == nil && !includesOtherArtistsContext(ctx, o.cat, intent, out.sequence.Tracks) {
		out.reserveReasons = append(out.reserveReasons, core.OutcomeReason{Code: "other_artists_missing", Detail: "The retained playlist does not include an eligible artist outside the named references.", Action: "Add another fitting reference, broaden the search, or remove the request to include other artists."})
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err == nil && key != "" && out.complete(intent.Count) {
		*o.assemblyCache = completedAssembly{key: key, result: out}
	}
	return out, err
}
