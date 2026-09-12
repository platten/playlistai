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
	// Descriptive requests need scored alternatives: an uncalibrated preview
	// comparison makes a track rankable, not necessarily a good sound match.
	// Keep the output count separate from the bounded comparison pool.
	target := intent.Count - len(required)
	comparisonTarget := target
	if o.audioSession != nil && soundComparisonRequested(intent) {
		comparisonTarget = max(target, min(o.cfg.MaxCandidates, recommendationPoolSize(intent.Count, len(required))))
	}
	if len(audio.Clauses(intent)) > 0 && o.audioSession == nil && !o.enhanced {
		return nil, []core.PlaylistNotice{{Code: "audio_analysis_unavailable", Detail: "Install and enable music analysis in setup to check the requested musical characteristics, then retry."}}, nil
	}
	attempted := map[string]struct{}{}
	recordings := map[string]bool{}
	recent := append([]core.TrackRef(nil), request.RecentSelections...)
	var queue []core.Candidate
	retrievalInterrupted := false
	refill := func() error {
		batch, err := o.prepareRecommendationPool(ctx, initial, ports.RetrievalRequest{
			Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted,
		}, eligible, recordings, recommendationPoolSize(intent.Count, len(required)))
		initial = nil
		if err != nil {
			if ctx.Err() != nil || len(batch) == 0 {
				return err
			}
			retrievalInterrupted = true
			notices = append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Candidate retrieval was interrupted; retained candidates were still checked without relaxing requirements."})
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
	if stream != nil && len(initial) > 0 {
		// Existing sound matches take part before a metadata stream can fill N.
		queue = append([]core.Candidate(nil), initial...)
		initial = nil
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
		if stream != nil && len(queue) == 0 {
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
				if retrievalInterrupted {
					break // assess the retained pool once; do not repeat the failing read
				}
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
			if !assessment.Eligible && !o.enhancedMetadataFallback(ctx, candidate, intent) {
				continue
			}
			audio.ApplyScores(&candidate, assessment)
		} else if len(audio.Clauses(intent)) > 0 && o.enhanced && !o.enhancedMetadataFallback(ctx, candidate, intent) {
			continue
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
		if len(accepted) >= comparisonTarget {
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
	assembly, err := o.assembleCandidates(ctx, candidates, intent, request, references, required, waypoints, seed)
	if errors.Is(err, core.ErrRequiredTrackConflict) {
		return false, nil
	}
	// Soft diversity preferences rank the available pool; they must not prolong
	// analysis after sequencing has produced the requested valid track count.
	return assembly.complete(intent.Count), err
}

func soundComparisonRequested(intent core.MusicIntent) bool {
	for _, clause := range audio.Clauses(intent) {
		switch clause.Kind {
		case "mood", "texture", "description":
			return true
		case "instrumentation":
			if !core.WantsInstrumental(intent) || clause.Text != "instrumental" {
				return true
			}
		case "vocal":
			if !core.WantsInstrumental(intent) {
				return true
			}
		}
	}
	return false
}
