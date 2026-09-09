package multichannel

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const iterativeBudget = 15 * time.Minute
const iterativeAttempts = 1000

// collectIteratively keeps discovery and recommendation continuation separate
// from the user's references. Every newly pulled candidate passes the same
// semantic, hard-eligibility and preview checks before it can seed continuation.
func (o *Orchestrator) collectIteratively(parent context.Context, initial []core.Candidate, stream ports.MusicCandidateStream, intent core.MusicIntent, request ports.RecommendationRequest, eligible *eligibility, references, required, waypoints []core.TrackRef, seed int64) (out []core.Candidate, outNotices []core.PlaylistNotice, outErr error) {
	ctx, cancel := context.WithTimeout(parent, iterativeBudget)
	defer cancel()
	var accepted []core.Candidate
	var notices []core.PlaylistNotice
	go func() {
		select {
		case <-request.StopChecking:
			cancel()
		case <-ctx.Done():
		}
	}()
	// Stop/budget cancellation interrupts provider I/O as well as analysis, but
	// must not discard already accepted tracks. Parent cancellation still does.
	defer func() {
		if outErr != nil && ctx.Err() != nil && parent.Err() == nil {
			out, outErr = accepted, nil
			outNotices = append(outNotices, core.PlaylistNotice{Code: "discovery_stopped", Detail: "Discovery stopped; only eligible tracks were retained."})
		}
	}()
	// Give ranking and MMR actual alternatives instead of stopping at the first
	// N acceptable records. The existing time/attempt budgets still bound work.
	target := max(2*(intent.Count-len(required)), intent.Count-len(required)+8)
	if len(audio.Clauses(intent)) > 0 && o.audioSession == nil {
		return nil, []core.PlaylistNotice{{Code: "audio_analysis_unavailable", Detail: "Install and enable music analysis in setup to check the requested musical characteristics, then retry."}}, nil
	}
	attempted := map[string]struct{}{}
	recordings := map[string]bool{}
	recent := append([]core.TrackRef(nil), request.RecentSelections...)
	queue := append([]core.Candidate(nil), initial...)
	refill := func() error {
		batch, err := o.retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted})
		if err != nil {
			return err
		}
		queue = batch
		return nil
	}
	if len(required) == intent.Count {
		return accepted, notices, nil // required tracks were already validated
	}
	for attempts := 0; attempts < iterativeAttempts; attempts++ {
		if err := parent.Err(); err != nil {
			return nil, notices, err
		}
		select {
		case <-request.StopChecking:
			return accepted, append(notices, core.PlaylistNotice{Code: "discovery_stopped", Detail: "Stopped searching; only eligible tracks were retained."}), nil
		default:
		}
		if ctx.Err() != nil {
			notices = append(notices, core.PlaylistNotice{Code: "discovery_budget", Detail: "The search time limit was reached, not the end of the catalog. Retry to search further."})
			break
		}
		if o.audioSession != nil {
			snapshot := o.audioSession.Snapshot()
			if snapshot.Stopped || snapshot.BudgetExhausted {
				break
			}
		}
		var candidate core.Candidate
		if stream != nil {
			track, err := stream.Next(ctx)
			if err != nil {
				if parent.Err() != nil {
					return nil, notices, parent.Err()
				}
				if !errors.Is(err, io.EOF) {
					notices = append(notices, core.PlaylistNotice{Code: "discovery_unavailable", Detail: "Online artist discovery was interrupted; continuing with available catalog candidates."})
				}
				stream = nil
				queue = nil // refill with attempted IDs removed, including replay
				continue
			}
			candidate = core.Candidate{Track: track, Sources: []core.RetrievalEvidence{{Channel: "metadata_discovery", Rank: len(attempted) + 1, QueryWeight: 1}}}
			if source, ok := stream.(ports.MusicCandidateEvidence); ok {
				candidate.Sources = source.Evidence(track.ID)
			}
		} else {
			if len(queue) == 0 {
				if err := refill(); err != nil {
					if parent.Err() != nil {
						return nil, notices, parent.Err()
					}
					return accepted, append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Candidate retrieval stopped before the requested count was reached."}), nil
				}
				if len(queue) == 0 {
					break
				}
			}
			candidate, queue = queue[0], queue[1:]
		}
		if _, seen := attempted[candidate.Track.ID]; seen {
			// A provider/retriever that cannot advance must not spin forever.
			if stream == nil && len(queue) == 0 {
				break
			}
			continue
		}
		attempted[candidate.Track.ID] = struct{}{}
		meta, exists := o.cat.Meta(candidate.Track.ID)
		if !exists {
			continue
		}
		candidate.Track = meta.Ref
		key := core.ProvisionalRecordingKey(candidate.Track)
		if recordings[key] {
			continue
		}
		if !o.metadataEligible(candidate.Track, intent) {
			continue
		}
		batch, err := eligible.filter(ctx, []core.Candidate{candidate}, intent.Constraints.ExcludeSeedArtists)
		if err != nil {
			return nil, notices, err
		}
		if o.features != nil {
			batch, _, err = filterSemanticConstraints(ctx, o.features, batch, intent.HardConstraints)
			if err != nil {
				return nil, notices, err
			}
		}
		batch, _, _, err = o.scoreSemanticUnion(ctx, batch, intent)
		if err != nil {
			return nil, notices, err
		}
		if len(batch) == 0 {
			continue
		}
		candidate = batch[0]
		if request.Progress != nil {
			request.Progress.Report("generation", int64(min(len(accepted), target)), int64(target), "Checking candidates for the final selection")
		}
		if o.audioSession != nil {
			assessment, err := o.audioSession.Check(parent, candidate.Track, false)
			if err != nil {
				return nil, notices, err
			}
			if !assessment.Eligible {
				continue
			}
			audio.ApplyScores(&candidate, assessment)
		}
		batch, _, err = o.filterEssential(ctx, []core.Candidate{candidate}, intent.EssentialCriteria)
		if err != nil {
			return nil, notices, err
		}
		if len(batch) == 0 {
			continue
		}
		if stages := journeyStageCriteria(intent); intent.Mode == core.ModeJourney && len(stages) > 0 {
			placeable := false
			for _, stage := range stages {
				fit, _, err := o.filterJourneyStage(ctx, batch, stage, intent)
				if err != nil {
					return nil, notices, err
				}
				placeable = placeable || len(fit) > 0
			}
			if !placeable {
				continue
			}
		}
		recordings[key] = true
		accepted = append(accepted, candidate)
		recent = append(recent, candidate.Track)
		if request.OnChecked != nil && o.audioSession != nil {
			request.OnChecked(candidate.Track)
		}
		if len(accepted) >= target {
			complete, err := o.iterativeComplete(ctx, accepted, intent, request, references, required, waypoints, seed)
			if err != nil {
				return nil, notices, err
			}
			if complete {
				return accepted, notices, nil
			}
		}
		if stream == nil {
			if err := refill(); err != nil {
				if parent.Err() != nil {
					return nil, notices, parent.Err()
				}
				notices = append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Recommendation continuation was interrupted."})
				break
			}
		}
	}
	if len(accepted)+len(required) >= intent.Count {
		complete, err := o.iterativeComplete(parent, accepted, intent, request, references, required, waypoints, seed)
		if err != nil {
			return nil, notices, err
		}
		if complete {
			return accepted, notices, nil
		}
	}
	notices = append(notices, core.PlaylistNotice{Code: "discovery_partial", Detail: "The bounded discovery pool did not yield a complete eligible playlist. Missing previews, evidence and provider coverage can limit results.", Requested: intent.Count, Actual: len(accepted) + len(required)})
	return accepted, notices, nil
}

// Do not stop just because enough candidates passed CLAP: the final selector
// and ordering constraints must also be able to produce the requested count.
func (o *Orchestrator) iterativeComplete(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) (bool, error) {
	ranked, err := o.rankCandidates(ctx, append([]core.Candidate(nil), candidates...), ports.RankRequest{Intent: intent, Profile: request.Profile})
	if err != nil {
		return false, err
	}
	reserved, remaining, _, err := o.reserveJourneyStages(ctx, ranked, required, intent)
	if err != nil {
		return false, err
	}
	fixed := append([]core.TrackRef(nil), required...)
	for _, c := range reserved {
		fixed = append(fixed, c.Track)
	}
	selection, err := o.selector.Select(ctx, remaining, ports.SelectionRequest{Intent: intent, Required: fixed, Waypoints: waypoints, RecentSelections: request.RecentSelections, Count: intent.Count - len(fixed)})
	if err != nil {
		return false, err
	}
	selection.Candidates = append(reserved, selection.Candidates...)
	if len(selection.Candidates)+len(required) < intent.Count {
		return false, nil
	}
	membership, err := o.categoryMembership(ctx, append(candidatesForTracks(required), selection.Candidates...), intent)
	if err != nil {
		return false, err
	}
	trajectoryWaypoints := waypoints
	if len(trajectoryWaypoints) < 2 && len(required) >= 2 {
		trajectoryWaypoints = required
	}
	var trajectory ports.Trajectory
	if intent.Mode == core.ModeJourney && len(trajectoryWaypoints) >= 2 {
		trajectory = NewWaypointTrajectory(o.cat, trajectoryWaypoints)
	}
	sequence, err := o.sequencer.Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: selection.Candidates, Required: required, Waypoints: waypoints, ReferenceAnchors: references, RecentSelections: request.RecentSelections, Seed: seed, CategoryStages: membership, Trajectory: trajectory})
	if errors.Is(err, core.ErrRequiredTrackConflict) {
		return false, nil
	}
	return len(sequence.Tracks) == intent.Count, err
}
