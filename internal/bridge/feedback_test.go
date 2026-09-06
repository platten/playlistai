package bridge

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestGenerationRecordsExposuresWithoutPositiveFeedback(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)
	generated, err := api.GenerateFromPromptWithContext(context.Background(), "like Justice, 10 tracks", IntentSessionContext{SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := c.Feedback.ListFeedback(context.Background(), ports.FeedbackQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(generated.Playlist.Tracks) {
		t.Fatalf("events = %d, exposures = %d", len(events), len(generated.Playlist.Tracks))
	}
	for _, event := range events {
		if event.Type != core.FeedbackExposure || event.Scope != core.FeedbackScopeRequest ||
			event.RequestID != generated.Playlist.Reproducibility.ID || event.SessionID != "session" {
			t.Fatalf("generated track became preference feedback: %+v", event)
		}
	}
	profile, err := api.GetTasteProfile(context.Background(), "session", generated.Request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.ColdStart || profile.PositiveEvidence != 0 || profile.NegativeEvidence != 0 || profile.ExposureCount != len(events) {
		t.Fatalf("exposures affected taste profile: %+v", profile)
	}
}

func TestExplicitFeedbackScopesAndClear(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)
	ctx := context.Background()
	like, err := api.RecordFeedback(ctx, RecordFeedbackRequest{
		Type: core.FeedbackLike, TrackID: "seed0001", SessionID: "session",
		Context: core.FeedbackContext{Surface: "playlist", Position: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	less, err := api.RecordFeedback(ctx, RecordFeedbackRequest{
		Type: core.FeedbackLessLike, TrackID: "seed0002", RequestID: "request", SessionID: "session",
		Context: core.FeedbackContext{Surface: "playlist", Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if like.EventID == "" || less.EventID == "" || like.Profile.PositiveEvidence != 1 || less.Profile.RequestEvidence != 1 {
		t.Fatalf("feedback receipts = like %+v, less %+v", like, less)
	}
	events, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{})
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if events[0].Scope != core.FeedbackScopeDurable || events[1].Scope != core.FeedbackScopeRequest {
		t.Fatalf("default feedback scopes are wrong: %+v", events)
	}
	if events[0].Versions.Catalog == "" || events[0].Versions.Profile == "" || events[0].Versions.IntentSchema == 0 {
		t.Fatalf("feedback version context missing: %+v", events[0].Versions)
	}
	otherRequest, err := api.GetTasteProfile(ctx, "other-session", "other-request")
	if err != nil {
		t.Fatal(err)
	}
	if otherRequest.PositiveEvidence != 1 || otherRequest.RequestEvidence != 0 {
		t.Fatalf("request-local negative leaked into durable profile: %+v", otherRequest)
	}
	if err := api.ClearTasteData(ctx); err != nil {
		t.Fatal(err)
	}
	cleared, err := api.GetTasteProfile(ctx, "", "")
	if err != nil || !cleared.ColdStart || cleared.PositiveEvidence != 0 || cleared.NegativeEvidence != 0 {
		t.Fatalf("cleared profile = %+v, %v", cleared, err)
	}
}

func TestFeedbackRejectsImplicitPreviewEvents(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)
	if _, err := api.RecordFeedback(context.Background(), RecordFeedbackRequest{
		Type: "preview_failed", Scope: core.FeedbackScopeDurable, TrackID: "seed0001",
	}); err == nil {
		t.Fatal("failed preview must not be recorded as dislike feedback")
	}
}

func TestExportConfirmationRecordsTrackAcceptance(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)
	receipt, err := api.RecordTrackAcceptance(context.Background(), RecordAcceptanceRequest{
		TrackIDs:  []string{"seed0001", "seed0002", "seed0001"},
		RequestID: "request", SessionID: "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EventCount != 2 || receipt.Profile.RequestEvidence != 2 || receipt.Profile.PositiveEvidence != 0 {
		t.Fatalf("acceptance receipt = %+v", receipt)
	}
	events, err := c.Feedback.ListFeedback(context.Background(), ports.FeedbackQuery{RequestID: "request"})
	if err != nil || len(events) != 2 {
		t.Fatalf("acceptance events = %+v, %v", events, err)
	}
	for position, event := range events {
		if event.Type != core.FeedbackAccepted || event.Scope != core.FeedbackScopeRequest ||
			event.Context.Surface != "export" || event.Context.Position != position {
			t.Fatalf("acceptance context = %+v", event)
		}
	}
}
