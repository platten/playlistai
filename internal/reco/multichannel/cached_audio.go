package multichannel

import (
	"context"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// The cached channel is frozen once per request. Refills can reuse unselected
// hits, but never re-encode queries or rescan a changing analysis store.
type cachedAudioRetriever struct {
	base       ports.CandidateRetriever
	candidates []core.Candidate
}

func (r *cachedAudioRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	raw, err := r.base.Retrieve(ctx, request)
	if err != nil {
		return nil, err
	}
	raw = append([]core.Candidate(nil), raw...)
	excluded := map[string]bool{}
	for id := range request.AttemptedIDs {
		excluded[id] = true
	}
	for _, track := range request.RecentSelections {
		excluded[track.ID] = true
	}
	positions := map[string]int{}
	for i, c := range raw {
		positions[c.Track.ID] = i
	}
	for _, c := range r.candidates {
		if excluded[c.Track.ID] {
			continue
		}
		if i, exists := positions[c.Track.ID]; exists {
			raw[i].Sources = append(append([]core.RetrievalEvidence(nil), raw[i].Sources...), c.Sources...)
			raw[i].Scores.SemanticMatch, raw[i].Available.SemanticMatch = c.Scores.SemanticMatch, c.Available.SemanticMatch
			raw[i].Scores.SemanticNegativeMatch, raw[i].Available.SemanticNegativeMatch = c.Scores.SemanticNegativeMatch, c.Available.SemanticNegativeMatch
		} else {
			positions[c.Track.ID] = len(raw)
			raw = append(raw, c)
		}
	}
	return raw, nil
}
