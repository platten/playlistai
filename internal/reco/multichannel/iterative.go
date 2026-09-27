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
const enhancedCandidateLimit = 512
const enhancedChannelBatch = 32
const enhancedMinimumComparisons = 2 * enhancedChannelBatch

// Required recordings occupy one slot each in the 2N pool, just as they do in
// the final playlist. The saved intent/count always remains N.
func recommendationPoolSize(count, required int) int {
	if count <= required {
		return 0
	}
	return 2*count - required
}

// A duration-only request can need additions after mandatory recordings fill
// the parser's default count. Keep this bounded working batch separate from
// the saved count and leave room for at least one additional recording.
func recommendationBatchCount(intent core.MusicIntent, required int) int {
	if intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount() {
		return min(core.MaxCount, max(intent.Count, required+1))
	}
	return intent.Count
}

// collectIteratively keeps discovery and recommendation continuation separate
// from the user's references. Every newly pulled candidate passes the same
// semantic, hard-eligibility and preview checks before it can seed continuation.
func (o *Orchestrator) collectIteratively(parent context.Context, initial []core.Candidate, stream ports.MusicCandidateStream, intent core.MusicIntent, request ports.RecommendationRequest, eligible *eligibility, references, required, waypoints []core.TrackRef, seed int64) (out []core.Candidate, outNotices []core.PlaylistNotice, outErr error) {
	ctx, cancel := context.WithTimeout(parent, iterativeBudget)
	if o.enhanced {
		cancel()
		ctx, cancel = ports.GenerationWorkContext(parent)
	}
	defer cancel()
	started := ports.GenerationStarted(parent)
	if started.IsZero() {
		started = time.Now()
	}
	if prefetch, ok := stream.(ports.CandidatePrefetcher); ok {
		defer prefetch.StopPrefetch()
	}
	if o.enhanced && o.search == nil {
		o.search = &core.SearchSnapshot{}
	}
	var accepted []core.Candidate
	var notices []core.PlaylistNotice
	qualityComplete := false
	defer func() {
		if !o.enhanced || o.search == nil {
			return
		}
		stopped := false
		select {
		case <-request.StopChecking:
			stopped = true
		default:
		}
		switch {
		case parent.Err() != nil:
			o.search.StopReason = "canceled"
		case stopped:
			o.search.StopReason = "user_stop"
		case ctx.Err() != nil:
			o.search.StopReason = "deadline"
			outNotices = append(outNotices, core.PlaylistNotice{Code: "discovery_budget", Detail: "The search time limit was reached; completed eligible tracks were retained. The sources were not exhausted."})
		case qualityComplete:
			o.search.StopReason = "quality_target"
		case o.search.Considered >= enhancedCandidateLimit:
			o.search.StopReason = "candidate_limit"
		default:
			o.search.StopReason = "sources_exhausted"
			for _, notice := range outNotices {
				if notice.Code == "discovery_interrupted" || notice.Code == "retrieval_interrupted" || notice.Code == "discovery_budget" {
					o.search.StopReason = "source_interrupted"
					break
				}
			}
		}
		ports.ReportSearch(request.Progress, core.SearchProgress{Stage: "assembling", ElapsedMilliseconds: time.Since(started).Milliseconds(), CandidatesConsidered: o.search.Considered, CandidatesEligible: o.search.Eligible, CandidatesSupported: supportedCandidateCount(accepted), StoppingReason: o.search.StopReason})
	}()
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
		if outErr != nil && !errors.Is(outErr, ports.ErrDiscoveryGenerationMismatch) && ctx.Err() != nil && parent.Err() == nil {
			out, outErr = accepted, nil
			outNotices = append(outNotices, core.PlaylistNotice{Code: "discovery_stopped", Detail: "Discovery stopped; only eligible tracks were retained."})
		}
	}()
	// Descriptive requests need scored alternatives: an uncalibrated preview
	// comparison makes a track rankable, not necessarily a good sound match.
	// Keep the output count separate from the bounded comparison pool.
	batchCount := recommendationBatchCount(intent, len(required))
	target := max(0, batchCount-len(required))
	comparisonTarget := target
	if (o.audioSession != nil || o.enhanced && o.cfg.LibraryEvidenceEnabled) && soundComparisonRequested(intent) {
		comparisonTarget = max(target, min(o.cfg.MaxCandidates, recommendationPoolSize(batchCount, len(required))))
	}
	if len(audio.Clauses(intent)) > 0 && o.audioSession == nil && !o.enhanced {
		return nil, []core.PlaylistNotice{{Code: "audio_analysis_unavailable", Detail: "Install and enable music analysis in setup to check the requested musical characteristics, then retry."}}, nil
	}
	attempted := map[string]struct{}{}
	recordings := map[string]bool{}
	recent := append([]core.TrackRef(nil), request.RecentSelections...)
	var queue []core.Candidate
	type preparedCandidate struct {
		candidate  core.Candidate
		assessment core.AudioAssessment
		err        error
	}
	var prepared []preparedCandidate
	retrievalInterrupted := false
	orderPool := func(batch []core.Candidate) ([]core.Candidate, error) {
		ranked, err := o.rankCandidates(ctx, batch, ports.RankRequest{Intent: intent, Profile: request.Profile})
		if err != nil {
			return nil, err
		}
		if o.enhanced {
			return roundRobinCandidateSources(ranked), nil
		}
		selection, err := NewSelector(o.cat, o.cfg).shortlist(ctx, ranked, ports.SelectionRequest{
			Intent: intent, Required: required, Waypoints: waypoints, RecentSelections: recent,
			Count: min(o.cfg.MaxCandidates, recommendationPoolSize(batchCount, len(required))),
		})
		if err != nil {
			return nil, err
		}
		// Unselected IDs are not marked attempted: later bounded retrieval can
		// reconsider them, but they must not turn this 2N analysis queue into
		// the entire (up to MaxCandidates) raw retrieval union.
		return selection.Candidates, nil
	}
	var pendingLocal []core.Candidate
	providerTurn := false
	providerOpportunity := stream == nil
	stalledPulls := 0
	refill := func() error {
		if o.enhanced {
			// Drain one local page before entering potentially slow provider I/O.
			// Provider candidates are assessed individually so a later slow read
			// cannot erase the successful prefix of a provider page.
			if providerTurn && stream != nil {
				providerTurn = false
				providerOpportunity = true
				track, err := stream.Next(ctx)
				if err == nil {
					next := core.Candidate{Track: track, Sources: []core.RetrievalEvidence{{Channel: "metadata_discovery", Rank: len(attempted) + 1, QueryWeight: 1}}}
					if source, ok := stream.(ports.MusicCandidateEvidence); ok {
						next.Sources = source.Evidence(track.ID)
					}
					queue = append(queue, next)
					return nil
				}
				if errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
					return err
				}
				if !errors.Is(err, io.EOF) {
					notices = append(notices, core.PlaylistNotice{Code: "discovery_interrupted", Detail: "Online discovery was interrupted; completed candidates were retained. The source was not exhausted."})
				}
				if prefetch, ok := stream.(ports.CandidatePrefetcher); ok {
					prefetch.StopPrefetch()
				}
				stream = nil
			}
			if len(pendingLocal) == 0 && !retrievalInterrupted {
				batch, err := o.prepareRecommendationPool(ctx, initial, ports.RetrievalRequest{Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted}, eligible, recordings, enhancedChannelBatch)
				initial = nil
				if err != nil {
					if errors.Is(err, ports.ErrDiscoveryGenerationMismatch) || ctx.Err() != nil {
						return err
					}
					// A failed channel must not terminate advancing healthy channels.
					// Empty or nonadvancing partial pages still stop this refill path.
					retrievalInterrupted = len(batch) == 0
					notices = append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "A retrieval source was interrupted; its completed candidates were retained."})
				}
				unseen := batch[:0]
				for _, candidate := range batch {
					if _, seen := attempted[candidate.Track.ID]; !seen {
						unseen = append(unseen, candidate)
					}
				}
				var rankErr error
				pendingLocal, rankErr = orderPool(unseen)
				if rankErr != nil {
					return rankErr
				}
			}
			queue, pendingLocal = nextCandidateRound(pendingLocal)
			providerTurn = stream != nil
			// The caller continues to an enabled provider even for an empty local page.
			return nil
		}
		if request.Progress != nil && len(accepted) > 0 {
			request.Progress.Report("generation", int64(min(len(accepted), target)), int64(target), "Finding additional similar tracks with Deej-AI")
		}
		batch, err := o.prepareRecommendationPool(ctx, initial, ports.RetrievalRequest{
			Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted,
		}, eligible, recordings, recommendationPoolSize(batchCount, len(required)))
		initial = nil
		if err != nil {
			if ctx.Err() != nil || len(batch) == 0 {
				return err
			}
			retrievalInterrupted = true
			notices = append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Candidate retrieval was interrupted; retained candidates were still checked without relaxing requirements."})
		}
		// Give MMR alternatives before early stopping: taking only the first N
		// passing relevance-ranked tracks would leave artist diversity no choice.
		batch = prioritizeJourneySupply(batch, intent)
		priority := 0
		if intent.Mode == core.ModeJourney && len(journeyStageCriteria(intent)) > 1 {
			priority = len(batch)
		}
		var ordered []core.Candidate
		ordered, err = orderPool(batch[priority:])
		if err != nil {
			return err
		}
		queue = append(queue, batch[:priority]...)
		queue = append(queue, ordered...)
		if request.Progress != nil {
			request.Progress.Report("generation", int64(len(queue)+len(required)), int64(2*batchCount), "Prepared recommendation shortlist; checking musical fit")
		}
		return nil
	}
	if stream != nil && len(initial) > 0 && !o.enhanced {
		// Existing sound matches take part before a metadata stream can fill N.
		queue = append([]core.Candidate(nil), initial...)
		initial = nil
		if concentratedArtistPool(queue, intent) {
			var err error
			queue, err = o.prepareRecommendationPool(ctx, queue, ports.RetrievalRequest{
				Intent: intent, Profile: request.Profile, RecentSelections: recent, Seed: seed, AttemptedIDs: attempted,
			}, eligible, recordings, recommendationPoolSize(batchCount, len(required)))
			if err != nil {
				return nil, notices, err
			}
		}
	}
	if len(required) == intent.Count && (intent.DurationSeconds <= 0 || intent.HasExplicitTrackCount()) {
		return accepted, notices, nil // required tracks were already validated
	}
	attempts := 0
	analysisLimited := false
	analysisStopped := false
	for ; attempts < iterativeAttempts; attempts++ {
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
		// Rejections also complete comparisons. A supported pool must be able
		// to reach its quality target when later unknown genres are omitted.
		if o.enhanced && len(accepted) >= target && providerOpportunity && len(prepared) == 0 && len(attempted) >= max(enhancedMinimumComparisons, 4*batchCount) && (len(attempted)%enhancedChannelBatch == 0 || len(queue) == 0) {
			assembly, err := o.assembleCandidates(ctx, accepted, intent, request, references, required, waypoints, seed)
			if err != nil && !errors.Is(err, core.ErrRequiredTrackConflict) && !errors.Is(err, errJourneySearchExhausted) {
				return nil, notices, err
			}
			if err == nil && o.qualityTargetMet(ctx, assembly, intent) {
				qualityComplete = true
				return accepted, notices, nil
			}
		}

		audioStopped := o.audioSession != nil && o.audioSession.ShouldStop()
		if audioStopped {
			if !analysisLimited {
				analysisStopped = o.audioSession.Snapshot().Stopped
				notices = append(notices, core.PlaylistNotice{Code: "analysis_limit", Detail: "Audio checking stopped or reached its analysis budget; the catalog was not exhausted."})
				analysisLimited = true
			}
			// A preview budget does not consume already packed evidence. Explicit
			// stops still terminate; all remaining candidates retain eligibility checks.
			if !o.enhanced || analysisStopped {
				break
			}
			// Optional preview acquisition limits do not end metadata discovery
			// or checking completed packed/cache evidence.
		}
		var (
			candidate          core.Candidate
			preparedAssessment core.AudioAssessment
			preparedErr        error
		)
		prechecked := len(prepared) > 0
		if prechecked {
			candidate = prepared[0].candidate
			preparedAssessment = prepared[0].assessment
			preparedErr = prepared[0].err
			prepared = prepared[1:]
		} else if stream != nil && len(queue) == 0 && !o.enhanced {
			pullLimit := 1
			for pulled := 0; pulled < pullLimit; pulled++ {
				track, err := stream.Next(ctx)
				if err != nil {
					if errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
						return nil, notices, err
					}
					if parent.Err() != nil {
						return nil, notices, parent.Err()
					}
					if !errors.Is(err, io.EOF) {
						notices = append(notices, core.PlaylistNotice{Code: "discovery_unavailable", Detail: "Online artist discovery was interrupted; continuing with available catalog candidates."})
					}
					if prefetch, ok := stream.(ports.CandidatePrefetcher); ok {
						prefetch.StopPrefetch()
					}
					stream = nil
					break
				}
				next := core.Candidate{Track: track, Sources: []core.RetrievalEvidence{{Channel: "metadata_discovery", Rank: len(attempted) + pulled + 1, QueryWeight: 1}}}
				if source, ok := stream.(ports.MusicCandidateEvidence); ok {
					next.Sources = source.Evidence(track.ID)
				}
				queue = append(queue, next)
			}
			if len(queue) == 0 {
				continue
			}
			candidate, queue = queue[0], queue[1:]
		} else {
			if len(queue) == 0 {
				if retrievalInterrupted && (!o.enhanced || len(pendingLocal) == 0 && stream == nil) {
					break // assess the retained pool once; do not repeat the failing read
				}
				before := len(attempted)
				if err := refill(); err != nil {
					if errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
						return nil, notices, err
					}
					if parent.Err() != nil {
						return nil, notices, parent.Err()
					}
					return accepted, append(notices, core.PlaylistNotice{Code: "retrieval_interrupted", Detail: "Candidate retrieval stopped before the requested count was reached."}), nil
				}
				if len(queue) == 0 {
					if o.enhanced && providerTurn {
						continue
					}
					if len(attempted) > before {
						continue // an entirely excluded page is not catalog exhaustion
					}
					break
				}
			}
			candidate, queue = queue[0], queue[1:]
		}
		if !prechecked {
			if o.enhanced && !o.admitCandidateAssessment(candidate) {
				attempted[candidate.Track.ID] = struct{}{}
				if len(queue) == 0 && len(pendingLocal) == 0 {
					break
				}
				continue
			}
			if _, seen := attempted[candidate.Track.ID]; seen {
				// A provider/retriever that cannot advance must not spin forever.
				if !o.enhanced && stream == nil && len(queue) == 0 {
					break
				}
				if o.enhanced {
					stalledPulls++
					if stalledPulls >= enhancedChannelBatch {
						notices = append(notices, core.PlaylistNotice{Code: "discovery_budget", Detail: "Discovery stopped because a source repeated candidates without advancing; the catalog was not exhausted."})
						break
					}
				}
				continue
			}
			stalledPulls = 0
			attempted[candidate.Track.ID] = struct{}{}
			if o.enhanced && o.search != nil {
				stage := "checking"
				if len(accepted) >= target {
					stage = "comparing"
				}
				ports.ReportSearch(request.Progress, core.SearchProgress{Stage: stage, ElapsedMilliseconds: time.Since(started).Milliseconds(), CandidatesConsidered: o.search.Considered, CandidatesEligible: len(accepted), CandidatesSupported: supportedCandidateCount(accepted)})
			}
		}
		var (
			key   string
			batch []core.Candidate
			err   error
		)
		if prechecked {
			key = core.ProvisionalRecordingKey(candidate.Track)
			if recordings[key] {
				continue
			}
		} else {
			var keep bool
			candidate, key, keep, err = o.prepareIterativeCandidate(ctx, candidate, intent, eligible, recordings)
			if err != nil {
				return nil, notices, err
			}
			if !keep {
				continue
			}
		}
		if request.Progress != nil {
			message := "Checking candidates for the final selection"
			if o.enhanced && len(accepted) >= target {
				message = "Comparing matches"
			}
			request.Progress.Report("generation", int64(min(len(accepted), target)), int64(target), message)
		}
		if packed, ok := o.packedOnly(ctx, candidate.Track.ID, intent); ok {
			audio.ApplyScores(&candidate, packed)
		} else if audioStopped {
			if !o.enhancedMetadataFallback(ctx, candidate, intent) {
				o.recordCandidateDecision(ctx, candidate, intent, "rejected", "missing_evidence_after_audio_budget")
				continue
			}
		} else if o.audioSession != nil {
			// In Enhanced Hybrid, pinned catalog evidence can fully establish a
			// candidate's requested facets. Do not spend network/provider budget
			// obtaining a redundant preview in that case.
			if !o.previewNeededForEnhanced(ctx, candidate, intent) {
				// The ordinary essential filters below still enforce the request.
			} else {
				var assessment core.AudioAssessment
				if prechecked {
					assessment, err = preparedAssessment, preparedErr
				} else if o.audioSession.Parallelism() > 1 && (stream == nil || o.enhanced && len(queue) > 0) {
					batch := []core.Candidate{candidate}
					var batchSlots []int
					for len(batch) < o.audioSession.Parallelism() && len(queue) > 0 && (!o.enhanced || len(attempted) < enhancedCandidateLimit) {
						next := queue[0]
						queue = queue[1:]
						if _, seen := attempted[next.Track.ID]; seen {
							continue
						}
						attempted[next.Track.ID] = struct{}{}
						if o.enhanced && !o.admitCandidateAssessment(next) {
							continue
						}
						next, _, keep, prepareErr := o.prepareIterativeCandidate(ctx, next, intent, eligible, recordings)
						if prepareErr != nil {
							return nil, notices, prepareErr
						}
						if keep {
							if packed, ok := o.packedOnly(ctx, next.Track.ID, intent); ok {
								prepared = append(prepared, preparedCandidate{candidate: next, assessment: packed})
								continue
							}
							batchSlots = append(batchSlots, len(prepared))
							prepared = append(prepared, preparedCandidate{candidate: next})
							batch = append(batch, next)
						}
					}
					tracks := make([]core.TrackRef, len(batch))
					for index := range batch {
						tracks[index] = batch[index].Track
					}
					assessments, checkErrs := o.audioSession.CheckMany(ctx, tracks, false)
					assessment = assessments[0]
					err = checkErrs[0]
					for index := 1; index < len(batch); index++ {
						prepared[batchSlots[index-1]] = preparedCandidate{candidate: batch[index], assessment: assessments[index], err: checkErrs[index]}
					}
				} else {
					assessment, err = o.audioSession.Check(ctx, candidate.Track, false)
				}
				if err != nil {
					return nil, notices, err
				}
				if !assessment.Eligible && !o.enhancedMetadataFallback(ctx, candidate, intent) {
					o.recordCandidateDecision(ctx, candidate, intent, "rejected", "audio_or_strict_criterion_unverified")
					continue
				}
				audio.ApplyScores(&candidate, assessment)
			}
		} else if len(audio.Clauses(intent)) > 0 && o.enhanced && !o.enhancedMetadataFallback(ctx, candidate, intent) {
			o.recordCandidateDecision(ctx, candidate, intent, "rejected", "musical_evidence_unavailable")
			continue
		}
		batch, err = o.filterConfirmedOutput(ctx, []core.Candidate{candidate}, intent)
		if err != nil {
			return nil, notices, err
		}
		if len(batch) == 0 {
			continue
		}
		batch, _, err = o.filterEssential(ctx, batch, intent.EssentialCriteria)
		if err != nil {
			return nil, notices, err
		}
		if len(batch) == 0 {
			o.recordCandidateDecision(ctx, candidate, intent, "rejected", "essential_criterion_unverified_or_contradicted")
			continue
		}
		candidate = batch[0]
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
				o.recordCandidateDecision(ctx, candidate, intent, "rejected", "no_eligible_journey_stage")
				continue
			}
		}
		recordings[key] = true
		if o.enhanced && o.bestAvailable {
			candidate.FitTier, candidate.MatchDetail = o.enhancedTier(ctx, candidate, intent)
		}
		candidate = o.recordCandidateDecision(ctx, candidate, intent, "eligible", "")
		accepted = append(accepted, candidate)
		if o.enhanced && o.search != nil {
			o.search.Eligible = len(accepted)
			o.rememberCandidateAssessment(candidate)
			stage := "checking"
			if len(accepted) >= target {
				stage = "comparing"
			}
			ports.ReportSearch(request.Progress, core.SearchProgress{Stage: stage, ElapsedMilliseconds: time.Since(started).Milliseconds(), CandidatesConsidered: o.search.Considered, CandidatesEligible: len(accepted), CandidatesSupported: supportedCandidateCount(accepted)})
		}
		recent = append(recent, candidate.Track)
		if request.OnChecked != nil && o.audioSession != nil {
			request.OnChecked(candidate.Track)
		}
		if !o.enhanced && (len(accepted) >= comparisonTarget || o.durationReadyToCheckContext(ctx, accepted, required, intent)) {
			complete, err := o.iterativeComplete(ctx, accepted, intent, request, references, required, waypoints, seed)
			if err != nil {
				return nil, notices, err
			}
			if complete {
				return accepted, notices, nil
			}
		}
		if !o.enhanced && intent.DurationSeconds > 0 && len(accepted)+len(required) >= o.cfg.MaxCandidates {
			notices = append(notices, core.PlaylistNotice{Code: "discovery_budget", Detail: "The candidate budget was reached before the requested duration was fulfilled; the catalog was not exhausted."})
			break // keep duration alternatives inside the configured candidate budget
		}
		// Keep the rest of the shortlist: refilling after every passing track
		// discards the 2N pool and needlessly repeats similarity queries.
	}
	if o.enhanced && o.search != nil && o.search.Considered >= enhancedCandidateLimit {
		notices = append(notices, core.PlaylistNotice{Code: "discovery_budget", Detail: "The 512-candidate comparison limit was reached; the catalog was not exhausted."})
	}
	if attempts == iterativeAttempts {
		notices = append(notices, core.PlaylistNotice{Code: "discovery_budget", Detail: "The discovery attempt budget was reached; the catalog was not exhausted."})
	}
	if len(accepted)+len(required) >= intent.Count || intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount() {
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

func (o *Orchestrator) prepareIterativeCandidate(ctx context.Context, candidate core.Candidate, intent core.MusicIntent, eligible *eligibility, recordings map[string]bool) (core.Candidate, string, bool, error) {
	meta, exists := ports.CatalogMeta(ctx, o.cat, candidate.Track.ID)
	if err := ctx.Err(); err != nil {
		return candidate, "", false, err
	}
	if !exists {
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "catalog_identity_unavailable")
		return candidate, "", false, nil
	}
	candidate.Track = meta.Ref
	key := core.ProvisionalRecordingKey(candidate.Track)
	if recordings[key] {
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "duplicate_recording")
		return candidate, key, false, nil
	}
	batch, err := eligible.filter(ctx, []core.Candidate{candidate}, intent.Constraints.ExcludeSeedArtists)
	if err != nil {
		return candidate, key, false, err
	}
	if len(batch) == 0 {
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "excluded_recording_or_artist")
		return candidate, key, false, nil
	}
	if err := o.verifyRecording(ctx, candidate.Track, intent); err != nil {
		return candidate, key, false, err
	}
	if !o.metadataEligibleContext(ctx, candidate.Track, intent) {
		if err := ctx.Err(); err != nil {
			return candidate, key, false, err
		}
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "metadata_constraint_conflict")
		return candidate, key, false, nil
	}
	if o.features != nil {
		batch, _, err = filterSemanticConstraints(ctx, o.features, batch, intent.HardConstraints)
		if err != nil {
			return candidate, key, false, err
		}
		if len(batch) == 0 {
			o.recordCandidateDecision(ctx, candidate, intent, "rejected", "strict_semantic_constraint_unverified")
			return candidate, key, false, nil
		}
	}
	batch, _, _, err = o.scoreSemanticUnion(ctx, batch, intent)
	if err != nil || len(batch) == 0 {
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "semantic_comparison_unavailable")
		return candidate, key, false, err
	}
	previewCannotConfirm := o.previewCannotConfirmGenre(ctx, candidate.Track.ID, intent)
	if err := ctx.Err(); err != nil {
		return candidate, key, false, err
	}
	if previewCannotConfirm {
		o.recordCandidateDecision(ctx, candidate, intent, "rejected", "requested_genre_unconfirmed")
		return candidate, key, false, ctx.Err()
	}
	return batch[0], key, true, nil
}

// Do not stop just because enough candidates passed CLAP: the final selector
// and ordering constraints must also be able to produce the requested count.
func (o *Orchestrator) iterativeComplete(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef, seed int64) (bool, error) {
	assembly, err := o.assembleCandidates(ctx, candidates, intent, request, references, required, waypoints, seed)
	if errors.Is(err, core.ErrRequiredTrackConflict) || errors.Is(err, errJourneySearchExhausted) {
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

// Seed planning and iterative comparison share one cumulative admission ledger.
// Reusing an already observed candidate never spends another assessment slot.
func (o *Orchestrator) admitCandidateAssessment(candidate core.Candidate) bool {
	if !o.enhanced || o.search == nil {
		return true
	}
	if o.hasCandidateAssessment(candidate.Track.ID) {
		return true
	}
	if o.search.Considered >= enhancedCandidateLimit {
		return false
	}
	if candidate.Decision == "" {
		candidate.Decision = "considered"
	}
	o.search.Candidates = append(o.search.Candidates, candidate)
	o.search.Considered = len(o.search.Candidates)
	return true
}

func (o *Orchestrator) rememberCandidateAssessment(candidate core.Candidate) {
	if o.search == nil {
		return
	}
	for i := range o.search.Candidates {
		if o.search.Candidates[i].Track.ID == candidate.Track.ID {
			o.search.Candidates[i] = candidate
			return
		}
	}
}

func (o *Orchestrator) hasCandidateAssessment(id string) bool {
	if o.search == nil {
		return false
	}
	for _, previous := range o.search.Candidates {
		if previous.Track.ID == id {
			return true
		}
	}
	return false
}

func supportedCandidateCount(candidates []core.Candidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.FitTier == fitStrong {
			count++
		}
	}
	return count
}
