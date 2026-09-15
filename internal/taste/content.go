package taste

import (
	"context"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/core"
)

// ContentFeedback keeps the latest explicit preference per catalog track ID, with
// matching request feedback ahead of durable history. Exposure is never a vote.
func ContentFeedback(events []core.FeedbackEvent, profile core.TasteProfile) []core.FeedbackEvent {
	out, _ := effectiveFeedback(context.Background(), orderedFeedback(events), ProfileOptions{RequestID: profile.RequestID, SessionID: profile.SessionID})
	return out
}

func orderedFeedback(events []core.FeedbackEvent) []core.FeedbackEvent {
	ordered := append([]core.FeedbackEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].OccurredAt.Equal(ordered[j].OccurredAt) {
			return ordered[i].OccurredAt.Before(ordered[j].OccurredAt)
		}
		// Legacy unstored fixtures retain input order on ties. Persisted events
		// always have a stable sequence, including those migrated from old stores.
		return ordered[i].Sequence < ordered[j].Sequence
	})
	return ordered
}

// Direct preference (like/dislike, more/less) is state, not an accumulating
// tally. Acceptance/removal is separate review state and contributes its weaker
// signal only when there is no direct preference in that scope. Matching
// request state overrides durable state for the same track, in both models.
func effectiveFeedback(ctx context.Context, ordered []core.FeedbackEvent, options ProfileOptions) ([]core.FeedbackEvent, error) {
	type states struct {
		direct, review map[string]core.FeedbackEvent
	}
	newStates := func() states { return states{map[string]core.FeedbackEvent{}, map[string]core.FeedbackEvent{}} }
	durableState, requestState := newStates(), newStates()
	for i, e := range ordered {
		if i&255 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, _, ok := feedbackWeight(e.Type); !ok {
			continue
		}
		var state states
		switch {
		case e.Scope == core.FeedbackScopeDurable:
			state = durableState
		case e.Scope == core.FeedbackScopeRequest && matchesRequest(e, options):
			state = requestState
		default:
			continue
		}
		if e.Type == core.FeedbackAccepted || e.Type == core.FeedbackRemoved {
			state.review[e.TrackID] = e
		} else {
			state.direct[e.TrackID] = e
		}
	}
	collapse := func(state states) map[string]core.FeedbackEvent {
		for id, event := range state.direct {
			state.review[id] = event
		}
		return state.review
	}
	durable, request := collapse(durableState), collapse(requestState)
	for id, e := range request {
		durable[id] = e
	}
	out := make([]core.FeedbackEvent, 0, len(durable))
	for _, e := range durable {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrackID < out[j].TrackID })
	return out, ctx.Err()
}

// ContentCentroids projects cached compatible representations only. It neither
// retrieves previews nor treats exposure as preference. At least two positive
// catalog tracks are needed; one explicit dislike can contribute a penalty.
func ContentCentroids(events []core.FeedbackEvent, profile core.TasteProfile, model core.AudioRepresentationIdentity, cached map[string]core.AudioRepresentation) (positive, negative []float32) {
	if model.Dimension <= 0 || model.Dimension > 16384 {
		return nil, nil
	}
	sums := [4][]float64{}
	counts := [4]int{}
	for i := range sums {
		sums[i] = make([]float64, model.Dimension)
	}
	for _, e := range ContentFeedback(events, profile) {
		r, ok := cached[e.TrackID]
		if !ok || r.Model != model || r.CatalogVersion != profile.CatalogVersion || len(r.Pooled) != model.Dimension {
			continue
		}
		polarity, weight, ok := feedbackWeight(e.Type)
		if !ok {
			continue
		}
		norm := 0.0
		for _, v := range r.Pooled {
			norm += float64(v) * float64(v)
		}
		if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			continue
		}
		group := 0
		if polarity < 0 {
			group = 1
		}
		if e.Scope == core.FeedbackScopeRequest {
			group += 2
		}
		for i, v := range r.Pooled {
			sums[group][i] += weight * float64(v) / math.Sqrt(norm)
		}
		counts[group]++
	}
	project := func(group, minimum int) []float32 {
		if counts[group+2] > 0 {
			group += 2
		}
		if counts[group] < minimum {
			return nil
		}
		norm := 0.0
		for _, v := range sums[group] {
			norm += v * v
		}
		if norm == 0 {
			return nil
		}
		out := make([]float32, model.Dimension)
		for i, v := range sums[group] {
			out[i] = float32(v / math.Sqrt(norm))
		}
		return out
	}
	return project(0, 2), project(1, 1)
}
