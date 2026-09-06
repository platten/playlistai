package multichannel

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// GreedySequencer orders an already selected set. It starts from a recent
// continuation track or original reference anchor, greedily maximizes measured
// transition similarity, relevance, and optional waypoint-trajectory fit, then
// performs bounded pair swaps that improve the same transition objective.
type GreedySequencer struct {
	cat ports.Catalog
	cfg Config
}

type sequenceItem struct {
	track     core.TrackRef
	candidate *core.Candidate
	required  bool
	fixed     bool
}

func NewSequencer(cat ports.Catalog, cfg Config) *GreedySequencer {
	return &GreedySequencer{cat: cat, cfg: cfg.normalized()}
}

func (s *GreedySequencer) Sequence(ctx context.Context, request ports.SequenceRequest) (ports.SequenceResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.SequenceResult{}, err
	}
	var items []sequenceItem
	var hardExhausted bool
	if request.Intent.Mode == core.ModeJourney && len(request.Required) >= 2 {
		items, hardExhausted = s.journeyWithRequiredAnchors(ctx, request)
	} else {
		items, hardExhausted = s.greedyFromPrefix(ctx, request)
	}
	if err := ctx.Err(); err != nil {
		return ports.SequenceResult{}, err
	}
	items = s.improve(items, request)
	if !s.hardSpacingValid(items, request) {
		return ports.SequenceResult{}, fmt.Errorf("%w: required ordering violates no-back-to-back artist", core.ErrRequiredTrackConflict)
	}

	result := ports.SequenceResult{
		Tracks: make([]core.TrackRef, 0, len(items)), Rationale: make([]core.StepReason, 0, len(items)),
		Notices: []core.PlaylistNotice{},
	}
	softRelaxations := 0
	gap := s.softArtistGap(request.Intent)
	for index, item := range items {
		result.Tracks = append(result.Tracks, item.track)
		relaxed := gap > 0 && artistSeenWithin(items, index, gap)
		if item.required {
			reason := requiredReason(item.track)
			if relaxed {
				softRelaxations++
				reason.Detail += " · soft artist spacing relaxed for required placement"
				reason.Evidence = append(reason.Evidence, core.ComponentEvidence{Component: "artist_spacing_relaxed", Available: true, Detail: "required track overrides soft artist-diversity preference"})
			}
			result.Rationale = append(result.Rationale, reason)
			continue
		}
		if relaxed {
			softRelaxations++
		}
		result.Rationale = append(result.Rationale, s.candidateReason(*item.candidate, request, index, len(items), relaxed))
	}
	if softRelaxations > 0 {
		result.Notices = append(result.Notices, core.PlaylistNotice{
			Code:      "soft_artist_spacing_relaxed",
			Detail:    fmt.Sprintf("soft artist spacing was relaxed for %d track(s); hard adjacency rules remained enforced", softRelaxations),
			Requested: len(items), Actual: len(items),
		})
	}
	if hardExhausted {
		result.Notices = append(result.Notices, core.PlaylistNotice{
			Code:      "hard_artist_spacing_exhausted",
			Detail:    "selected candidates could not be fully ordered without violating the hard no-back-to-back artist rule",
			Requested: request.Intent.Count, Actual: len(items),
		})
	}
	if len(request.Intent.Journey.EnergyTrajectory) > 0 {
		result.Notices = append(result.Notices, core.PlaylistNotice{
			Code:      "energy_trajectory_unsupported",
			Detail:    "energy trajectory was preserved but not applied because the catalog has no acoustic energy feature",
			Requested: request.Intent.Count, Actual: len(items),
		})
	}
	return result, nil
}

func (s *GreedySequencer) greedyFromPrefix(ctx context.Context, request ports.SequenceRequest) ([]sequenceItem, bool) {
	items := make([]sequenceItem, 0, len(request.Required)+len(request.Candidates))
	for _, track := range request.Required {
		items = append(items, sequenceItem{track: track, required: true, fixed: true})
	}
	remaining := append([]core.Candidate(nil), request.Candidates...)
	previous := s.startAnchor(request, items)
	for len(remaining) > 0 {
		candidate, next, ok := s.pick(ctx, remaining, previous, core.TrackRef{}, items, request, len(items))
		if !ok {
			return items, true
		}
		items = append(items, sequenceItem{track: candidate.Track, candidate: &candidate})
		remaining = next
		previous = candidate.Track
	}
	return items, false
}

func (s *GreedySequencer) journeyWithRequiredAnchors(ctx context.Context, request ports.SequenceRequest) ([]sequenceItem, bool) {
	items := make([]sequenceItem, 0, len(request.Required)+len(request.Candidates))
	remaining := append([]core.Candidate(nil), request.Candidates...)
	perSegment := distribute(len(remaining), len(request.Required)-1)
	previous := s.startAnchor(request, nil)
	hardExhausted := false
	for segment := 0; segment < len(request.Required)-1; segment++ {
		start, end := request.Required[segment], request.Required[segment+1]
		if segment == 0 {
			items = append(items, sequenceItem{track: start, required: true, fixed: true})
			previous = start
		}
		for index := range perSegment[segment] {
			avoidNext := core.TrackRef{}
			if index == perSegment[segment]-1 {
				avoidNext = end
			}
			candidate, next, ok := s.pick(ctx, remaining, previous, avoidNext, items, request, len(items))
			if !ok {
				hardExhausted = true
				break
			}
			items = append(items, sequenceItem{track: candidate.Track, candidate: &candidate})
			remaining = next
			previous = candidate.Track
		}
		items = append(items, sequenceItem{track: end, required: true, fixed: true})
		previous = end
	}
	return items, hardExhausted
}

func (s *GreedySequencer) pick(ctx context.Context, candidates []core.Candidate, previous, avoidNext core.TrackRef, items []sequenceItem, request ports.SequenceRequest, position int) (core.Candidate, []core.Candidate, bool) {
	if err := ctx.Err(); err != nil {
		return core.Candidate{}, candidates, false
	}
	allowed := make([]int, 0, len(candidates))
	spaced := make([]int, 0, len(candidates))
	gap := s.softArtistGap(request.Intent)
	for index, candidate := range candidates {
		if request.Intent.Constraints.NoRepeatArtistBackToBack && previous.ID != "" && sameArtist(previous, candidate.Track) {
			continue
		}
		if request.Intent.Constraints.NoRepeatArtistBackToBack && avoidNext.ID != "" && sameArtist(avoidNext, candidate.Track) {
			continue
		}
		allowed = append(allowed, index)
		if gap == 0 || !artistInTail(items, candidate.Track.Artist, gap) {
			spaced = append(spaced, index)
		}
	}
	pool := spaced
	if len(pool) == 0 {
		pool = allowed // only a soft preference is relaxed here
	}
	if len(pool) == 0 {
		return core.Candidate{}, candidates, false
	}
	chosen, best := pool[0], math.Inf(-1)
	for _, index := range pool {
		score := s.orderingScore(candidates[index], previous, request, position)
		if score > best || (score == best && candidates[index].Track.ID < candidates[chosen].Track.ID) {
			chosen, best = index, score
		}
	}
	candidate := candidates[chosen]
	remaining := make([]core.Candidate, 0, len(candidates)-1)
	remaining = append(remaining, candidates[:chosen]...)
	remaining = append(remaining, candidates[chosen+1:]...)
	return candidate, remaining, true
}

func (s *GreedySequencer) orderingScore(candidate core.Candidate, previous core.TrackRef, request ports.SequenceRequest, position int) float64 {
	score := s.cfg.TransitionRelevanceWeight * candidate.Scores.SelectionRelevance
	if similarity, ok := s.trackSimilarity(previous, candidate.Track, request.Intent); ok {
		score += request.Intent.Controls.TransitionSmoothness * similarity
	}
	if request.Trajectory != nil && request.Intent.Count > 1 {
		if target, ok := request.Trajectory.Target(float64(position) / float64(request.Intent.Count-1)); ok {
			if vectors, exists := s.cat.Vectors(candidate.Track.ID); exists {
				if similarity, measured := weightedVectorSimilarity(vectors, target, request.Intent.Controls.AudioWeight, request.Intent.Controls.CooccurrenceWeight); measured {
					score += s.cfg.JourneyPositionWeight * similarity
				}
			}
		}
	}
	return score
}

func (s *GreedySequencer) startAnchor(request ports.SequenceRequest, items []sequenceItem) core.TrackRef {
	if len(items) > 0 {
		return items[len(items)-1].track
	}
	if len(request.RecentSelections) > 0 {
		return request.RecentSelections[len(request.RecentSelections)-1]
	}
	if len(request.ReferenceAnchors) > 0 {
		return request.ReferenceAnchors[0]
	}
	return core.TrackRef{}
}

func (s *GreedySequencer) improve(items []sequenceItem, request ports.SequenceRequest) []sequenceItem {
	if len(items) < 3 || s.cfg.LocalImprovementPasses == 0 {
		return items
	}
	best := s.sequenceObjective(items, request)
	for range s.cfg.LocalImprovementPasses {
		changed := false
		for left := 0; left < len(items)-1; left++ {
			if items[left].fixed {
				continue
			}
			limit := minInt(len(items), left+s.cfg.LocalImprovementWindow+1)
			for right := left + 1; right < limit; right++ {
				if items[right].fixed {
					continue
				}
				items[left], items[right] = items[right], items[left]
				objective := s.sequenceObjective(items, request)
				if s.hardSpacingValid(items, request) && objective > best+1e-9 {
					best, changed = objective, true
				} else {
					items[left], items[right] = items[right], items[left]
				}
			}
		}
		if !changed {
			break
		}
	}
	return items
}

func (s *GreedySequencer) sequenceObjective(items []sequenceItem, request ports.SequenceRequest) float64 {
	var score float64
	previous := s.startAnchor(request, nil)
	for index, item := range items {
		if similarity, ok := s.trackSimilarity(previous, item.track, request.Intent); ok {
			score += request.Intent.Controls.TransitionSmoothness * similarity
		}
		if request.Trajectory != nil && len(items) > 1 {
			if target, ok := request.Trajectory.Target(float64(index) / float64(len(items)-1)); ok {
				if vectors, exists := s.cat.Vectors(item.track.ID); exists {
					if similarity, measured := weightedVectorSimilarity(vectors, target, request.Intent.Controls.AudioWeight, request.Intent.Controls.CooccurrenceWeight); measured {
						score += s.cfg.JourneyPositionWeight * similarity
					}
				}
			}
		}
		previous = item.track
	}
	gap := s.softArtistGap(request.Intent)
	for index := range items {
		if gap > 0 && artistSeenWithin(items, index, gap) {
			score -= .1 * request.Intent.Controls.ArtistDiversity
		}
	}
	return score
}

func (s *GreedySequencer) trackSimilarity(left, right core.TrackRef, intent core.MusicIntent) (float64, bool) {
	if left.ID == "" || right.ID == "" {
		return 0, false
	}
	a, aOK := s.cat.Vectors(left.ID)
	b, bOK := s.cat.Vectors(right.ID)
	if !aOK || !bOK {
		return 0, false
	}
	return weightedVectorSimilarity(a, b, intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight)
}

func (s *GreedySequencer) softArtistGap(intent core.MusicIntent) int {
	if intent.Controls.ArtistDiversity <= 0 || s.cfg.SoftArtistSpacingMax == 0 {
		return 0
	}
	return maxInt(1, int(math.Round(intent.Controls.ArtistDiversity*float64(s.cfg.SoftArtistSpacingMax))))
}

func (s *GreedySequencer) hardSpacingValid(items []sequenceItem, request ports.SequenceRequest) bool {
	if !request.Intent.Constraints.NoRepeatArtistBackToBack {
		return true
	}
	previous := s.startAnchor(request, nil)
	for _, item := range items {
		if previous.ID != "" && sameArtist(previous, item.track) {
			return false
		}
		previous = item.track
	}
	return true
}

func (s *GreedySequencer) candidateReason(candidate core.Candidate, request ports.SequenceRequest, position, total int, spacingRelaxed bool) core.StepReason {
	evidence := rankingEvidence(candidate, request.Intent, s.cfg)
	evidence = append(evidence,
		core.ComponentEvidence{Component: "selection_relevance", Score: candidate.Scores.SelectionRelevance, Weight: 1, Available: candidate.Available.SelectionRelevance},
		core.ComponentEvidence{Component: "embedding_redundancy", Score: candidate.Scores.EmbeddingRedundancy, Weight: -s.cfg.EmbeddingRedundancyWeight, Available: candidate.Available.EmbeddingRedundancy},
		core.ComponentEvidence{Component: "artist_concentration", Score: candidate.Scores.ArtistConcentration, Weight: -s.cfg.ArtistConcentrationWeight, Available: candidate.Available.ArtistConcentration},
		core.ComponentEvidence{Component: "album_concentration", Score: candidate.Scores.AlbumConcentration, Weight: -s.cfg.AlbumConcentrationWeight, Available: candidate.Available.AlbumConcentration},
		core.ComponentEvidence{Component: "mmr_selection", Score: candidate.Scores.MMR, Weight: 1, Available: candidate.Available.MMR},
	)
	parts := []string{fmt.Sprintf("rank %.3f · MMR %.3f", candidate.Scores.Total, candidate.Scores.MMR)}
	if request.Trajectory != nil && total > 1 {
		if target, ok := request.Trajectory.Target(float64(position) / float64(total-1)); ok {
			if vectors, exists := s.cat.Vectors(candidate.Track.ID); exists {
				score, measured := weightedVectorSimilarity(vectors, target, request.Intent.Controls.AudioWeight, request.Intent.Controls.CooccurrenceWeight)
				evidence = append(evidence, core.ComponentEvidence{Component: "embedding_trajectory", Score: score, Weight: s.cfg.JourneyPositionWeight, Available: measured, Detail: request.Trajectory.Evidence()})
			}
		}
	}
	if spacingRelaxed {
		parts = append(parts, "soft artist spacing relaxed")
		evidence = append(evidence, core.ComponentEvidence{Component: "artist_spacing_relaxed", Available: true, Detail: "soft artist-diversity preference only"})
	}
	return core.StepReason{TrackID: candidate.Track.ID, Kind: "selected", Detail: strings.Join(parts, " · "), Sources: candidate.Sources, Evidence: evidence}
}

func rankingEvidence(candidate core.Candidate, intent core.MusicIntent, cfg Config) []core.ComponentEvidence {
	return []core.ComponentEvidence{
		{Component: "audio_seed_affinity", Score: candidate.Scores.AudioSeedAffinity, Weight: intent.Controls.AudioWeight, Available: candidate.Available.AudioSeedAffinity},
		{Component: "cooccurrence_seed_affinity", Score: candidate.Scores.CooccurrenceAffinity, Weight: intent.Controls.CooccurrenceWeight, Available: candidate.Available.CooccurrenceAffinity},
		{Component: "listener_affinity", Score: candidate.Scores.ListenerAffinity, Weight: cfg.ListenerWeight, Available: candidate.Available.ListenerAffinity},
		{Component: "negative_preference_match", Score: candidate.Scores.NegativeMatch, Weight: -cfg.NegativePenalty, Available: candidate.Available.NegativeMatch},
		{Component: "recent_exposure", Score: candidate.Scores.RecentExposure, Weight: -cfg.ExposurePenalty, Available: candidate.Available.RecentExposure},
		{Component: "listener_novelty", Score: candidate.Scores.Novelty, Weight: cfg.NoveltyWeight * intent.Controls.Discovery, Available: candidate.Available.Novelty},
		{Component: "reciprocal_rank_fusion", Score: candidate.Scores.RetrievalFusion, Weight: cfg.RetrievalWeight, Available: candidate.Available.RetrievalFusion},
	}
}

func requiredReason(track core.TrackRef) core.StepReason {
	return core.StepReason{
		TrackID: track.ID, Kind: "required", Detail: "required track",
		Sources:  []core.RetrievalEvidence{},
		Evidence: []core.ComponentEvidence{{Component: "required", Score: 1, Weight: 1, Available: true}},
	}
}

func artistInTail(items []sequenceItem, artist string, gap int) bool {
	key := core.NormalizeIdentityPart(artist)
	start := maxInt(0, len(items)-gap)
	for _, item := range items[start:] {
		if key != "" && core.NormalizeIdentityPart(item.track.Artist) == key {
			return true
		}
	}
	return false
}

func artistSeenWithin(items []sequenceItem, index, gap int) bool {
	if index <= 0 {
		return false
	}
	return artistInTail(items[:index], items[index].track.Artist, gap)
}

func sameArtist(left, right core.TrackRef) bool {
	artist := core.NormalizeIdentityPart(left.Artist)
	return artist != "" && artist == core.NormalizeIdentityPart(right.Artist)
}

var _ ports.PlaylistSequencer = (*GreedySequencer)(nil)
