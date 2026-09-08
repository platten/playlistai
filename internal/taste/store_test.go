package taste

import (
	"context"
	"fmt"
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
	if loaded, ok, err := reopened.ProfileByID(context.Background(), "snapshot"); err != nil || !ok || loaded.SnapshotID != "snapshot" {
		t.Fatalf("snapshot ID lookup failed: %+v %v", loaded, err)
	}
	if _, ok, err := reopened.ProfileByID(context.Background(), "missing"); err != nil || ok {
		t.Fatal("missing snapshot was not distinguished")
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

func exposureEvent(trackID, requestID, sessionID string, at time.Time) core.FeedbackEvent {
	return core.FeedbackEvent{
		OccurredAt: at, Type: core.FeedbackExposure, Scope: core.FeedbackScopeRequest,
		TrackID: trackID, RequestID: requestID, SessionID: sessionID,
	}
}

// Exposure volume scales with every generation, so a read must not grow with
// the number of playlists the user has ever made.
func TestListFeedbackBoundsExposuresByAgeAndCount(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	ctx := context.Background()
	stale := exposureEvent("old", "r-old", "s1", now.Add(-ExposureRetention-time.Hour))
	if _, err := store.RecordFeedback(ctx, stale); err != nil {
		t.Fatal(err)
	}
	fresh := make([]core.FeedbackEvent, 0, MaxExposureEvents+50)
	for i := range MaxExposureEvents + 50 {
		fresh = append(fresh, exposureEvent(
			fmt.Sprintf("t%05d", i), "r-new", "s1", now.Add(-time.Duration(i)*time.Minute)))
	}
	if err := store.RecordFeedbackBatch(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListFeedback(ctx, ports.FeedbackQuery{SessionID: "s1", IncludeExposures: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != MaxExposureEvents {
		t.Fatalf("exposures returned = %d, want the %d newest", len(events), MaxExposureEvents)
	}
	for _, event := range events {
		if event.TrackID == "old" {
			t.Fatal("returned an exposure older than ExposureRetention")
		}
		if event.OccurredAt.Before(now.Add(-ExposureRetention)) {
			t.Fatalf("returned an out-of-window exposure at %s", event.OccurredAt)
		}
	}
}

// A scoped profile must not absorb evidence that merely recorded no request or
// session of its own.
func TestListFeedbackScopeDoesNotMatchBlankColumns(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	// Generation records exposures with whatever session the caller had, which
	// may be blank. Those rows must not leak into a different request.
	unrelated := exposureEvent("unrelated", "r-other", "", now)
	mine := core.FeedbackEvent{
		OccurredAt: now, Type: core.FeedbackRemoved, Scope: core.FeedbackScopeRequest,
		TrackID: "mine", RequestID: "r1", SessionID: "",
	}
	durable := core.FeedbackEvent{
		OccurredAt: now, Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable,
		TrackID: "durable", RequestID: "", SessionID: "",
	}
	for _, event := range []core.FeedbackEvent{unrelated, mine, durable} {
		if _, err := store.RecordFeedback(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	events, err := store.ListFeedback(ctx, ports.FeedbackQuery{RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, event := range events {
		got[event.TrackID] = true
	}
	if got["unrelated"] {
		t.Fatal("a request-scoped query matched an event with no request or session")
	}
	if !got["mine"] || !got["durable"] {
		t.Fatalf("scoped query lost its own or durable evidence: %v", got)
	}
}

func TestPruneExposuresKeepsExplicitFeedback(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := store.RecordFeedback(ctx, exposureEvent("exposed", "r", "s", old)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFeedback(ctx, core.FeedbackEvent{
		OccurredAt: old, Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, TrackID: "loved",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneExposures(ctx, old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListFeedback(ctx, ports.FeedbackQuery{IncludeExposures: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TrackID != "loved" {
		t.Fatalf("prune must drop only exposures, got %+v", events)
	}
}

// A batch written faster than the platform clock advances gives every event the
// same occurred_at. Reads must still come back in insertion order: this is an
// append-only log, and ids are random, so an id tiebreak returns a different
// permutation every time. Ties are routine on Windows and possible anywhere.
func TestListFeedbackKeepsInsertionOrderWhenTimestampsTie(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixed := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }
	ctx := context.Background()

	const count = 8
	batch := make([]core.FeedbackEvent, 0, count)
	for i := range count {
		batch = append(batch, core.FeedbackEvent{
			Type: core.FeedbackAccepted, Scope: core.FeedbackScopeRequest,
			TrackID: fmt.Sprintf("track%d", i), RequestID: "request",
			Context: core.FeedbackContext{Surface: "export", Position: i},
		})
	}
	if err := store.RecordFeedbackBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListFeedback(ctx, ports.FeedbackQuery{RequestID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count {
		t.Fatalf("got %d events, want %d", len(events), count)
	}
	for index, event := range events {
		if !event.OccurredAt.Equal(fixed) {
			t.Fatalf("event %d did not tie on occurred_at: %s", index, event.OccurredAt)
		}
		if event.Context.Position != index || event.TrackID != fmt.Sprintf("track%d", index) {
			t.Fatalf("event %d out of insertion order: %+v", index, event)
		}
	}
}
