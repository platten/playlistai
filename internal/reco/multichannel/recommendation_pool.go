package multichannel

import (
	"context"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Bound by retrieval evidence, never source membership. Local and outside
// recordings compete on the same scale before final musical ranking.
func boundedMetadataCandidates(candidates []core.Candidate, limit int) []core.Candidate {
	ordered := append([]core.Candidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Scores.RetrievalFusion != ordered[j].Scores.RetrievalFusion {
			return ordered[i].Scores.RetrievalFusion > ordered[j].Scores.RetrievalFusion
		}
		return ordered[i].Track.ID < ordered[j].Track.ID
	})
	return ordered[:min(len(ordered), max(0, limit))]
}

// Top up a shortlist before analysis when identity exclusions or duplicate
// recordings shrink retrieval pages. Provisional candidates are excluded only
// from preparation queries, not continuation: unselected alternatives remain
// available for a later refill. The request context and query bound cap work.
func (o *Orchestrator) prepareRecommendationPool(ctx context.Context, initial []core.Candidate, request ports.RetrievalRequest, eligible *eligibility, acceptedRecordings map[string]bool, size int) ([]core.Candidate, error) {
	size = min(size, o.cfg.MaxCandidates)
	attempted := request.AttemptedIDs
	request.AttemptedIDs = make(map[string]struct{}, len(attempted)+size)
	for id := range attempted {
		request.AttemptedIDs[id] = struct{}{}
	}
	seenRecordings := make(map[string]bool, size)
	var pool []core.Candidate
	for queries := 0; queries < iterativeAttempts && len(pool) < size; queries++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := initial
		initial = nil
		if len(raw) == 0 {
			var err error
			raw, err = o.retriever.Retrieve(ctx, request)
			if err != nil {
				// A later page failure must not erase already prepared candidates.
				// The caller can still assess them, unless cancellation forbids work.
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return pool, err
			}
		}
		before := len(request.AttemptedIDs)
		// A provider's concatenation order must not decide which source owns
		// the bounded pool. Compare common retrieval evidence before truncation.
		raw = boundedMetadataCandidates(raw, len(raw))
		for _, candidate := range raw {
			id := candidate.Track.ID
			if _, seen := request.AttemptedIDs[id]; seen {
				continue
			}
			request.AttemptedIDs[id] = struct{}{}
			meta, exists := o.cat.Meta(id)
			if !exists {
				attempted[id] = struct{}{}
				continue
			}
			candidate.Track = meta.Ref
			key := core.ProvisionalRecordingKey(candidate.Track)
			batch, err := eligible.filter(ctx, []core.Candidate{candidate}, request.Intent.Constraints.ExcludeSeedArtists)
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 || acceptedRecordings[key] || seenRecordings[key] {
				attempted[id] = struct{}{}
				continue
			}
			seenRecordings[key] = true
			if len(pool) < o.cfg.MaxCandidates {
				pool = append(pool, candidate)
			}
		}
		if len(request.AttemptedIDs) == before {
			break // exhausted or non-advancing retrieval must not spin
		}
	}
	return pool, nil
}
