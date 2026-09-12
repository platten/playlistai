package multichannel

import (
	"context"

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
	}{candidates, intent, request.Profile, resolvedContextTracks(o.cat, request.RecentSelections), references, required, waypoints, seed, o.cfg, o.bestAvailable, o.knowledge, assessments, request.EnhancedAudio.Fingerprint() + o.enhancedSnapshot.Fingerprint() + EnhancedPolicyVersion})
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

func (o *Orchestrator) assembleCandidates(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) (candidateAssembly, error) {
	var out candidateAssembly
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := o.prepareEnhanced(ctx, candidates, intent, request, references, required, waypoints); err != nil {
		return out, err
	}
	request.EnhancedAudio = o.enhancedSnapshot
	key := ""
	if o.assemblyCache != nil {
		key = o.assemblyKey(candidates, intent, request, references, required, waypoints, seed)
		if key != "" && o.assemblyCache.key == key {
			return o.assemblyCache.result, nil
		}
	}
	ranked, err := o.rankCandidates(ctx, append([]core.Candidate(nil), candidates...), ports.RankRequest{Intent: intent, Profile: request.Profile, EnhancedAudio: request.EnhancedAudio})
	if err != nil {
		return out, err
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
	selectionCount := intent.Count
	if variableDuration {
		selectionCount = max(selectionCount, len(fixed))
	}
	recent := resolvedContextTracks(o.cat, request.RecentSelections)
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
		assessment := o.assessDuration(out.sequence.Tracks, intent)
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
				next := o.assessDuration(sequence.Tracks, intent)
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
	if err == nil && key != "" && out.complete(intent.Count) {
		*o.assemblyCache = completedAssembly{key: key, result: out}
	}
	return out, err
}
