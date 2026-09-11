package bridge

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"

	"github.com/platten/playlistai/internal/core"
)

const maxPresentations = 128

type presentation struct {
	events       []core.FeedbackEvent
	acknowledged bool
}

// A presentation is an ephemeral delivery capability, not a saved generation
// identity. Keeping only the event payload avoids retaining prompts or large
// analysis snapshots. The bounded registry also rejects invented track IDs:
// acknowledgment accepts a server-issued ID, never a caller-supplied track list.
type presentationStore struct {
	mu      sync.Mutex
	entries map[string]*presentation
	order   []string
}

func (a *API) preparePresentation(request BuildPlaylistRequest, result *PlaylistResult) {
	result.PresentationID = ""
	if len(result.Tracks) == 0 {
		return
	}
	id := rand.Text()
	entry := &presentation{events: a.exposureEvents(request, *result)}
	a.presentations.mu.Lock()
	defer a.presentations.mu.Unlock()
	if a.presentations.entries == nil {
		a.presentations.entries = make(map[string]*presentation)
	}
	if len(a.presentations.order) >= maxPresentations {
		delete(a.presentations.entries, a.presentations.order[0])
		a.presentations.order = a.presentations.order[1:]
	}
	a.presentations.entries[id] = entry
	a.presentations.order = append(a.presentations.order, id)
	result.PresentationID = id
}

// AcknowledgePlaylistDisplayed records exposure only after the current result
// appears on the playlist screen. Retries and React effect replays are harmless.
// Reopening history obtains a new presentation; navigation retains the old one.
func (a *API) AcknowledgePlaylistDisplayed(ctx context.Context, presentationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.presentations.mu.Lock()
	defer a.presentations.mu.Unlock()
	entry := a.presentations.entries[presentationID]
	if entry == nil {
		return errors.New("playlist presentation expired or was not issued by this application session")
	}
	if entry.acknowledged {
		return nil
	}
	if a.app.Feedback != nil {
		if err := a.app.Feedback.RecordFeedbackBatch(ctx, entry.events); err != nil {
			return err // leave unacknowledged so a failed transaction can be retried
		}
	}
	entry.acknowledged = true
	entry.events = nil
	return nil
}
