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

// Required recordings occupy one slot each in the 2N pool, just as they do in
// the final playlist. The saved intent/count always remains N.
func recommendationPoolSize(count, required int) int {
	if count <= required {
		return 0
	}
	return 2*count - required
}

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
	// Metadata discovery retains its bounded oversampling policy. Recommendation
	// continuation instead prepares a 2N shortlist BEFORE expensive analysis,
	// consumes it in recommendation order, and stops once selection can fill N.
	target := max(2*(intent.Count-len(required)), intent.Count-len(required)+8)
	if len(audio.Clauses(intent)) > 0 && o.audioSession == nil {
		return nil, []core.PlaylistNotice{{Code: "audio_analysis_unavailable", Detail: "Install and enable music analysis in setup to check the requested musical characteristics, then retry."}}, nil
	}
	attempted := map[string]struct{}{}
	recordings := map[string]bool{}
	recent := append([]core.TrackRef(nil), request.RecentSelections...)
	var queue []core.Candidate
	refill := func() error {
		batch, err := o.prepareRecommendationPool(ctx, initial, ports.RetrievalRequest{
			Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted,
		}, eligible, recordings, recommendationPoolSize(intent.Count, len(required)))
		initial = nil
		if err != nil {
			return err
		}
		batch, err = o.rankCandidates(ctx, batch, ports.RankRequest{Intent: intent, Profile: request.Profile})
		if err != nil {
			return err
		}
		// Give MMR alternatives before early stopping: taking only the first N
		// passing relevance-ranked tracks would leave artist diversity no choice.
		selection, err := NewSelector(o.cat, o.cfg).shortlist(ctx, batch, ports.SelectionRequest{
			Intent: intent, Required: required, Waypoints: waypoints, RecentSelections: recent,
			Count: recommendationPoolSize(intent.Count, len(required)),
		})
		if err != nil {
			return err
		}
		queue = selection.Candidates
		if request.Progress != nil {
			request.Progress.Report("generation", int64(len(queue)+len(required)), int64(2*intent.Count), "Prepared recommendation shortlist; checking musical fit")
		}
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
		if o.audioSession != nil && o.audioSession.ShouldStop() {
			break
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
			target = intent.Count - len(required)
			if len(queue) == 0 {
				before := len(attempted)
				if err := refill(); err != nil {
					if parent.Err() != nil {
						return nil, notices, parent.Err()
					}
					return accepted, append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Candidate retrieval stopped before the requested count was reached."}), nil
				}
				if len(queue) == 0 {
					if len(attempted) > before {
						continue // an entirely excluded page is not catalog exhaustion
					}
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
		// Keep the rest of the shortlist: refilling after every passing track
		// discards the 2N pool and needlessly repeats similarity queries.
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
	reserved, remaining, reasons, err := o.reserveJourneyStages(ctx, ranked, required, intent)
	if err != nil {
		return false, err
	}
	if len(reasons) > 0 {
		return false, nil // a full-count first stage is still an incomplete journey
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
	if (intent.Mode == core.ModeJourney || o.bestAvailable) && len(trajectoryWaypoints) >= 2 {
		trajectory = NewWaypointTrajectory(o.cat, trajectoryWaypoints)
	}
	sequence, err := o.sequencer.Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: selection.Candidates, Required: required, Waypoints: waypoints, ReferenceAnchors: references, RecentSelections: request.RecentSelections, Seed: seed, CategoryStages: membership, Trajectory: trajectory})
	if errors.Is(err, core.ErrRequiredTrackConflict) {
		return false, nil
	}
	for _, notice := range sequence.Notices {
		if notice.Code == "category_journey_exhausted" {
			return false, err
		}
	}
	if o.bestAvailable && genreArtistDiversity(intent) {
		artists := map[string]bool{}
		for _, track := range sequence.Tracks {
			if key := core.NormalizeIdentityPart(track.Artist); key != "" {
				artists[key] = true
			}
		}
		// Keep looking within the same bounded, eligible pool before settling
		// for a two-artist alternation. Exhaustion still returns safe partials.
		if len(artists) < min(3, intent.Count) {
			return false, err
		}
	}
	return len(sequence.Tracks) == intent.Count, err
}
