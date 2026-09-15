package taste

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestCorrectionsUseSameEffectiveFeedbackForDenseAndContent(t *testing.T) {
	for _, scope := range []core.FeedbackScope{core.FeedbackScopeDurable, core.FeedbackScopeRequest} {
		t.Run(string(scope), func(t *testing.T) {
			var events []core.FeedbackEvent
			kinds := []core.FeedbackType{core.FeedbackLike, core.FeedbackDislike, core.FeedbackLike}
			if scope == core.FeedbackScopeRequest {
				kinds = []core.FeedbackType{core.FeedbackMoreLike, core.FeedbackLessLike, core.FeedbackMoreLike}
			}
			for i, kind := range kinds {
				e := feedback(kind, scope, "a", "active", time.Unix(100, 0))
				e.Sequence = int64(i + 1)
				events = append(events, e)
			}
			options := ProfileOptions{RequestID: "active"}
			p, err := BuildProfile(context.Background(), profileCatalog(), events, options)
			if err != nil {
				t.Fatal(err)
			}
			content := ContentFeedback(events, p)
			if len(content) != 1 || content[0].Type != kinds[2] || content[0].Sequence != 3 {
				t.Fatalf("latest correction lost: %+v", content)
			}
			positive, negative := p.Positive.Audio, p.Negative.Audio
			if scope == core.FeedbackScopeRequest {
				positive, negative = p.RequestPositive.Audio, p.RequestNegative.Audio
			}
			if !reflect.DeepEqual(positive, []float32{1, 0}) || !reflect.DeepEqual(negative, []float32{0, 0}) {
				t.Fatalf("obsolete opposite preference survives: +%v -%v", positive, negative)
			}
			shuffled, err := BuildProfile(context.Background(), profileCatalog(), []core.FeedbackEvent{events[2], events[0], events[1]}, options)
			if err != nil || !reflect.DeepEqual(p, shuffled) {
				t.Fatalf("persisted sequence depends on input order: %v", err)
			}
		})
	}
}

func TestFeedbackReviewStateDoesNotUndoDirectPreference(t *testing.T) {
	now := time.Unix(100, 0)
	events := []core.FeedbackEvent{
		feedback(core.FeedbackDislike, core.FeedbackScopeDurable, "a", "", now),
		feedback(core.FeedbackAccepted, core.FeedbackScopeDurable, "a", "", now.Add(time.Second)),
		feedback(core.FeedbackAccepted, core.FeedbackScopeRequest, "b", "active", now),
		feedback(core.FeedbackRemoved, core.FeedbackScopeRequest, "b", "active", now.Add(time.Second)),
		feedback(core.FeedbackMoreLike, core.FeedbackScopeRequest, "a", "unrelated", now.Add(2*time.Second)),
		feedback(core.FeedbackExposure, core.FeedbackScopeRequest, "a", "active", now.Add(3*time.Second)),
	}
	profile := core.TasteProfile{RequestID: "active"}
	got := ContentFeedback(events, profile)
	if len(got) != 2 || got[0].Type != core.FeedbackDislike || got[1].Type != core.FeedbackRemoved {
		t.Fatalf("direct/review/scope precedence changed: %+v", got)
	}
	events = append(events, feedback(core.FeedbackMoreLike, core.FeedbackScopeRequest, "a", "active", now.Add(4*time.Second)))
	dense, err := BuildProfile(context.Background(), profileCatalog(), events, ProfileOptions{RequestID: "active"})
	if err != nil || dense.NegativeEvidence != 0 || dense.RequestEvidence != 2 || dense.ExposureCount != 1 {
		t.Fatalf("active correction failed to override durable state: %+v %v", dense, err)
	}
}

func TestFeedbackSequenceMigrationPreservesLegacyOrderAndSnapshots(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i, e := range []core.FeedbackEvent{
		{ID: "unrelated", Type: core.FeedbackLike, TrackID: "b"},
		{ID: "z-old-dislike", Type: core.FeedbackDislike, TrackID: "a"},
		{ID: "a-new-like", Type: core.FeedbackLike, TrackID: "a"},
	} {
		e.OccurredAt, e.Scope = time.Unix(100, 0), core.FeedbackScopeDurable
		stored, err := s.RecordFeedback(ctx, e)
		if err != nil || stored.Sequence != int64(i+1) {
			t.Fatalf("new store sequence: %+v %v", stored, err)
		}
	}
	old := core.TasteProfile{Version: 2, AlgorithmVersion: "taste-profile/v2", SnapshotID: "preserved-v2", CatalogVersion: "fake:v1", NegativeEvidence: 1}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO taste_profiles(snapshot_id,saved_at,catalog_version,profile_json) VALUES(?,?,?,?)`, old.SnapshotID, 100, old.CatalogVersion, raw); err != nil {
		t.Fatal(err)
	}
	// Construct the exact pre-sequence schema while retaining its existing
	// events and snapshots; migration must not rebuild either table.
	for _, statement := range []string{`DROP TRIGGER assign_feedback_sequence`, `DROP INDEX idx_feedback_sequence`, `ALTER TABLE feedback_events DROP COLUMN sequence`} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, statement := range []string{`DELETE FROM feedback_events WHERE id='unrelated'`, `VACUUM`} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListFeedback(ctx, ports.FeedbackQuery{})
	if err != nil || len(events) != 2 || events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("migration/VACUUM changed event order: %+v %v", events, err)
	}
	if got := ContentFeedback(events, core.TasteProfile{}); len(got) != 1 || got[0].Type != core.FeedbackLike {
		t.Fatalf("random ID overrode insertion order: %+v", got)
	}
	loaded, ok, err := s.ProfileByID(ctx, old.SnapshotID)
	if err != nil || !ok || !reflect.DeepEqual(old, loaded) {
		t.Fatalf("legacy snapshot rewritten: %+v %v", loaded, err)
	}
	// Old binaries omit sequence. The compatibility trigger assigns it before
	// a newer app reads the corrected preference back.
	if _, err := s.db.Exec(`INSERT INTO feedback_events(id,version,occurred_at,type,scope,track_id) VALUES('legacy-write',1,100000000000,'dislike','durable','a')`); err != nil {
		t.Fatal(err)
	}
	events, err = s.ListFeedback(ctx, ports.FeedbackQuery{})
	if err != nil || len(events) != 3 || events[2].Sequence != 4 {
		t.Fatalf("legacy writer sequence: %+v %v", events, err)
	}
	if got := ContentFeedback(events, core.TasteProfile{}); got[0].Type != core.FeedbackDislike {
		t.Fatalf("legacy writer correction lost: %+v", got)
	}
}
