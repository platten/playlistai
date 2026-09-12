package taste

import (
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestContentCentroidsUseExplicitCompatibleFeedback(t *testing.T) {
	m := core.AudioRepresentationIdentity{Model: "mert", Dimension: 2, Revision: "one"}
	p := core.TasteProfile{CatalogVersion: "catalog", RequestID: "active", SessionID: "same-session"}
	events := []core.FeedbackEvent{
		{ID: "1", TrackID: "a", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(1, 0)},
		{ID: "2", TrackID: "b", Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, OccurredAt: time.Unix(2, 0)},
		{ID: "3", TrackID: "c", Type: core.FeedbackExposure, Scope: core.FeedbackScopeRequest, RequestID: "active"},
	}
	cached := map[string]core.AudioRepresentation{}
	for _, id := range []string{"a", "b", "c"} {
		cached[id] = core.AudioRepresentation{TrackID: id, CatalogVersion: "catalog", Model: m, Pooled: []float32{1, 0}}
	}
	a := cached["c"]
	a.Pooled = []float32{0, 1}
	cached["c"] = a
	positive, negative := ContentCentroids(events, p, m, cached)
	if len(positive) != 2 || positive[0] != 1 || positive[1] != 0 || negative != nil {
		t.Fatalf("centroids %v %v", positive, negative)
	}
	// Matching request dislikes replace a durable vote; other requests do not.
	events = append(events, core.FeedbackEvent{ID: "4", TrackID: "a", Type: core.FeedbackDislike, Scope: core.FeedbackScopeRequest, RequestID: "active"}, core.FeedbackEvent{ID: "5", TrackID: "b", Type: core.FeedbackDislike, Scope: core.FeedbackScopeRequest, RequestID: "other", SessionID: "same-session"})
	positive, negative = ContentCentroids(events, p, m, cached)
	if positive != nil || len(negative) != 2 || negative[0] != 1 {
		t.Fatalf("request priority %v %v", positive, negative)
	}
	m.Revision = "other"
	positive, negative = ContentCentroids(events, p, m, cached)
	if positive != nil || negative != nil {
		t.Fatal("mixed model spaces")
	}
}
