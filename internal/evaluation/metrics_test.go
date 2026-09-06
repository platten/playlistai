package evaluation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestRecallAndNDCG(t *testing.T) {
	t.Parallel()
	relevance := map[string]float64{"a": 3, "b": 2, "c": 1}
	if got, ok := RecallAtK([]string{"a", "x", "b"}, relevance, 3); !ok || math.Abs(got-2.0/3) > 1e-9 {
		t.Fatalf("recall=%v ok=%v", got, ok)
	}
	best, _ := NDCGAtK([]string{"a", "b", "c"}, relevance, 3)
	worse, _ := NDCGAtK([]string{"c", "b", "a"}, relevance, 3)
	if best != 1 || worse >= best {
		t.Fatalf("ndcg best=%v worse=%v", best, worse)
	}
	if _, ok := RecallAtK(nil, map[string]float64{}, 10); ok {
		t.Fatal("empty labels must be unavailable")
	}
}

func TestPlaylistDiagnosticsAndHardViolations(t *testing.T) {
	t.Parallel()
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Artist - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "copy", Display: "Artist - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "b", Display: "Other - Two", Audio: []float32{0, 1}, Track: []float32{0, 1}})
	playlist := core.Playlist{Tracks: []core.TrackRef{mustRef(cat, "a"), mustRef(cat, "copy"), mustRef(cat, "b")}, Intent: core.MusicIntent{Controls: core.IntentControls{AudioWeight: .5, CooccurrenceWeight: .5}, HardConstraints: []core.HardConstraint{{Kind: "exclude_artist", Value: "Artist", RuntimeEnforced: true}, {Kind: "no_back_to_back_artist", Value: "true", RuntimeEnforced: true}}}}
	duplicates, diversity, share, coverage, repetition, transition := PlaylistDiagnostics(cat, playlist, []string{"b"})
	if duplicates != 1 || math.Abs(diversity-2.0/3) > 1e-9 || math.Abs(share-2.0/3) > 1e-9 || coverage != 1 || math.Abs(repetition-1.0/3) > 1e-9 || transition == nil {
		t.Fatalf("diagnostics=%d %.3f %.3f %.3f %.3f %v", duplicates, diversity, share, coverage, repetition, transition)
	}
	if got := HardConstraintViolations(context.Background(), playlist, nil); got != 3 {
		t.Fatalf("violations=%d", got)
	}
}

func mustRef(cat *fakes.Catalog, id string) core.TrackRef { meta, _ := cat.Meta(id); return meta.Ref }

func TestCaseRelevanceUsesExplicitOutcomesButNotExposures(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := RecommendationCase{ID: "request", ListenerID: "listener", OccurredAt: at, Relevance: map[string]float64{"labeled": 2}}
	events := []InteractionRecord{
		{ListenerID: "listener", Event: evaluationEvent("like", core.FeedbackLike, "liked", "request", at.Add(time.Minute))},
		{ListenerID: "listener", Event: evaluationEvent("exposure", core.FeedbackExposure, "shown", "request", at.Add(2*time.Minute))},
		{ListenerID: "listener", Event: evaluationEvent("remove", core.FeedbackRemoved, "liked", "request", at.Add(3*time.Minute))},
		{ListenerID: "other", Event: evaluationEvent("other", core.FeedbackLike, "wrong", "request", at.Add(time.Minute))},
	}
	got := caseRelevance(item, events)
	if got["labeled"] != 2 || got["liked"] != 0 {
		t.Fatalf("relevance=%v", got)
	}
	if _, found := got["shown"]; found {
		t.Fatal("exposure became positive evidence")
	}
}

func evaluationEvent(id string, kind core.FeedbackType, track, request string, at time.Time) core.FeedbackEvent {
	return core.FeedbackEvent{Version: core.FeedbackEventVersion, ID: id, OccurredAt: at, Type: kind, Scope: core.FeedbackScopeRequest, TrackID: track, RequestID: request}
}
