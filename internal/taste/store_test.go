package taste

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestFeedbackAndProfilePersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 2, 3, 4, 5, time.UTC)
	event, err := store.RecordFeedback(context.Background(), core.FeedbackEvent{
		OccurredAt: now, Type: core.FeedbackRemoved, Scope: core.FeedbackScopeRequest,
		TrackID: "track", RequestID: "request", SessionID: "session",
		Context:  core.FeedbackContext{Surface: "review", Position: 3, RationaleKind: "nearest"},
		Versions: core.FeedbackVersions{Catalog: "catalog", Recommendation: "reco", IntentSchema: 5, Profile: ProfileAlgorithmVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.ID == "" || event.Version != core.FeedbackEventVersion {
		t.Fatalf("store did not complete event identity: %+v", event)
	}
	profile := core.TasteProfile{
		Version: ProfileContractVersion, AlgorithmVersion: ProfileAlgorithmVersion,
		SnapshotID: "snapshot", CatalogVersion: "catalog", RequestID: "request", SessionID: "session",
		Clusters: []core.TasteCluster{},
	}
	if err := store.SaveProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	otherContext := profile
	otherContext.RequestID = "other-request"
	otherContext.SessionID = "other-session"
	if err := store.SaveProfile(context.Background(), otherContext); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	events, err := reopened.ListFeedback(context.Background(), ports.FeedbackQuery{RequestID: "request", SessionID: "session"})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if events[0].Context.Position != 3 || events[0].Versions.Catalog != "catalog" || !events[0].OccurredAt.Equal(now) {
		t.Fatalf("event fields did not round trip: %+v", events[0])
	}
	loaded, ok, err := reopened.LatestProfile(context.Background(), "catalog", "request", "session")
	if err != nil || !ok || loaded.SnapshotID != profile.SnapshotID {
		t.Fatalf("profile = %+v, ok=%v err=%v", loaded, ok, err)
	}
	if _, ok, err := reopened.LatestProfile(context.Background(), "catalog", "other-request", "other-session"); err != nil || !ok {
		t.Fatalf("same snapshot was not retained for another context: ok=%v err=%v", ok, err)
	}
	if err := reopened.ClearFeedback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ClearProfiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, _ = reopened.ListFeedback(context.Background(), ports.FeedbackQuery{})
	if len(events) != 0 {
		t.Fatalf("feedback was not cleared: %+v", events)
	}
	if _, ok, _ := reopened.LatestProfile(context.Background(), "catalog", "request", "session"); ok {
		t.Fatal("profile was not cleared")
	}
}

func TestFeedbackValidationRejectsImplicitPreviewJudgments(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, err = store.RecordFeedback(context.Background(), core.FeedbackEvent{
		Type: "preview_paused", Scope: core.FeedbackScopeDurable, TrackID: "track",
	})
	if err == nil {
		t.Fatal("preview telemetry must not become preference feedback")
	}
}
