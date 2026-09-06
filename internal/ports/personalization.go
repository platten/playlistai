package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

type FeedbackQuery struct {
	RequestID string
	SessionID string
}

// FeedbackStore persists append-only interaction evidence locally.
type FeedbackStore interface {
	RecordFeedback(ctx context.Context, event core.FeedbackEvent) (core.FeedbackEvent, error)
	RecordFeedbackBatch(ctx context.Context, events []core.FeedbackEvent) error
	ListFeedback(ctx context.Context, query FeedbackQuery) ([]core.FeedbackEvent, error)
	ClearFeedback(ctx context.Context) error
}

// ProfileStore persists deterministic profile snapshots independently from
// raw events so callers can inspect or replay the exact projection used.
type ProfileStore interface {
	SaveProfile(ctx context.Context, profile core.TasteProfile) error
	LatestProfile(ctx context.Context, catalogVersion, requestID, sessionID string) (core.TasteProfile, bool, error)
	ClearProfiles(ctx context.Context) error
}
