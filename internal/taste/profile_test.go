package taste

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func profileCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Alpha - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "a2", Display: "Alpha - Two", Audio: []float32{.98, .02}, Track: []float32{.98, .02}},
		fakes.CatalogTrack{ID: "b", Display: "Beta - One", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
}

func feedback(kind core.FeedbackType, scope core.FeedbackScope, trackID, requestID string, at time.Time) core.FeedbackEvent {
	return core.FeedbackEvent{
		Version: core.FeedbackEventVersion, ID: string(kind) + "-" + trackID + "-" + requestID,
		OccurredAt: at, Type: kind, Scope: scope, TrackID: trackID,
		RequestID: requestID, SessionID: "session", Context: core.FeedbackContext{Surface: "test"},
	}
}

func TestExposureIsNotPositiveFeedback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	profile, err := BuildProfile(context.Background(), profileCatalog(), []core.FeedbackEvent{
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "a", "request", now),
	}, ProfileOptions{RequestID: "request", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if !profile.ColdStart || profile.PositiveEvidence != 0 || profile.RequestEvidence != 0 || profile.ExposureCount != 1 {
		t.Fatalf("exposure changed affinity evidence: %+v", profile)
	}
}

func TestExplicitEventSemantics(t *testing.T) {
	t.Parallel()
	cases := map[core.FeedbackType]struct {
		polarity int
		weight   float64
	}{
		core.FeedbackLike:     {1, 1},
		core.FeedbackDislike:  {-1, 1},
		core.FeedbackMoreLike: {1, .8},
		core.FeedbackLessLike: {-1, .8},
		core.FeedbackAccepted: {1, .35},
		core.FeedbackRemoved:  {-1, .35},
	}
	for kind, want := range cases {
		polarity, weight, ok := feedbackWeight(kind)
		if !ok || polarity != want.polarity || weight != want.weight {
			t.Errorf("%s = (%d, %v, %v), want (%d, %v, true)", kind, polarity, weight, ok, want.polarity, want.weight)
		}
	}
	if _, _, ok := feedbackWeight(core.FeedbackExposure); ok {
		t.Fatal("an exposure must not have affinity weight")
	}
}

func TestExposureCountsRespectRequestContext(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "a", "request-1", now),
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "b", "request-2", now),
	}
	contextual, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{
		RequestID: "request-1", SessionID: "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	global, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if contextual.ExposureCount != 1 || global.ExposureCount != 2 {
		t.Fatalf("exposure scopes were mixed: contextual=%d global=%d", contextual.ExposureCount, global.ExposureCount)
	}
}

func TestGenerationExposureProfileIsGlobalDecayedAndReproducible(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "a", "request-1", now.Add(-exposureHalfLife)),
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "b", "request-2", now),
	}
	options := ProfileOptions{RequestID: "new-request", SessionID: "session", IncludeAllExposures: true}
	first, err := BuildProfile(context.Background(), profileCatalog(), events, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildProfile(context.Background(), profileCatalog(), []core.FeedbackEvent{events[1], events[0]}, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.ExposureCount != 2 || first.RecentExposures["b"] <= first.RecentExposures["a"] {
		t.Fatalf("global exposure recency was not preserved: %+v", first)
	}
	if first.SnapshotID != second.SnapshotID {
		t.Fatalf("exposure snapshot is not reproducible: %s != %s", first.SnapshotID, second.SnapshotID)
	}
}

func TestProfileDecayFavorsRecentEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	profile, err := BuildProfile(context.Background(), profileCatalog(), []core.FeedbackEvent{
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "a", "", now.Add(-profileHalfLife)),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "b", "", now),
	}, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Positive.Audio[1] <= profile.Positive.Audio[0] {
		t.Fatalf("recent evidence did not receive more weight: %v", profile.Positive.Audio)
	}
}

func TestProfileIsReproducibleAndPreservesMultipleInterests(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "a", "", now),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "a2", "", now),
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "b", "", now),
	}
	first, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{SessionID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildProfile(context.Background(), profileCatalog(), []core.FeedbackEvent{events[2], events[0], events[1]}, ProfileOptions{SessionID: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID {
		t.Fatalf("same durable evidence produced different snapshots: %s != %s", first.SnapshotID, second.SnapshotID)
	}
	if len(first.Clusters) != 2 {
		t.Fatalf("clusters = %d, want two distinct interests: %+v", len(first.Clusters), first.Clusters)
	}
}

func TestRequestNegativeDoesNotBecomeDurableDislike(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackDislike, core.FeedbackScopeDurable, "a", "", now),
		feedback(core.FeedbackLessLike, core.FeedbackScopeRequest, "b", "request-1", now),
	}
	durable, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{RequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{RequestID: "request-2"})
	if err != nil {
		t.Fatal(err)
	}
	if durable.NegativeEvidence != 1 || durable.RequestEvidence != 0 || request.RequestEvidence != 1 || other.RequestEvidence != 0 {
		t.Fatalf("scope separation failed: durable=%+v request=%+v other=%+v", durable, request, other)
	}
}

func TestExplicitRequestAffinityOverridesHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	catalog := profileCatalog()
	profile, err := BuildProfile(context.Background(), catalog, []core.FeedbackEvent{
		feedback(core.FeedbackLike, core.FeedbackScopeDurable, "a", "", now),
	}, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	explicit := AffinityFromIntent(catalog, core.MusicIntent{
		Version: core.CurrentIntentVersion,
		References: []core.IntentReference{{
			Kind: core.ReferenceTrack, TrackID: "a", Influence: core.InfluenceNegative,
		}},
	})
	candidate, _ := catalog.Vectors("a")
	score := ScoreCandidate(profile, candidate, explicit)
	if score.Historical <= 0 || score.Effective >= 0 {
		t.Fatalf("explicit request did not override conflicting history: %+v", score)
	}
}

func TestColdStartIsStableWithoutEvidence(t *testing.T) {
	t.Parallel()
	first, err := BuildProfile(context.Background(), profileCatalog(), nil, ProfileOptions{SessionID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildProfile(context.Background(), profileCatalog(), nil, ProfileOptions{SessionID: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.ColdStart || first.SnapshotID == "" || first.SnapshotID != second.SnapshotID || len(first.Clusters) != 0 {
		t.Fatalf("cold start is not stable: first=%+v second=%+v", first, second)
	}
}

func TestProfileBuildHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildProfile(ctx, profileCatalog(), nil, ProfileOptions{}); err != context.Canceled {
		t.Fatalf("BuildProfile error = %v, want context.Canceled", err)
	}
}
