package bridge

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/ports"
)

func TestPresentationAcknowledgmentRejectsUnknownCanceledAndCleared(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	ctx := context.Background()
	if err := a.AcknowledgePlaylistDisplayed(ctx, "invented"); err == nil {
		t.Fatal("unissued presentation accepted")
	}
	result, err := a.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.AcknowledgePlaylistDisplayed(canceled, result.Playlist.PresentationID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.ClearTasteData(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.AcknowledgePlaylistDisplayed(ctx, result.Playlist.PresentationID); err == nil {
		t.Fatal("late display resurrected cleared taste data")
	}
}

func TestHistoryReopenHasNewPresentationWithoutChangingGeneration(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	ctx := context.Background()
	generated, err := a.GenerateFromPrompt(ctx, "like Justice, 3 tracks")
	if err != nil {
		t.Fatal(err)
	}
	history, err := a.ListSavedPlaylists()
	if err != nil || len(history) != 1 {
		t.Fatalf("history: %v %v", history, err)
	}
	loaded, err := a.LoadSavedPlaylist(history[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result.PresentationID == generated.Playlist.PresentationID || loaded.Result.Reproducibility.ID != generated.Playlist.Reproducibility.ID {
		t.Fatal("presentation identity was confused with generation identity")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := a.AcknowledgePlaylistDisplayed(ctx, loaded.Result.PresentationID); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	events, err := a.app.Feedback.ListFeedback(ctx, ports.FeedbackQuery{})
	if err != nil || len(events) != len(loaded.Result.Tracks) {
		t.Fatalf("duplicate acknowledgment: %d events, %v", len(events), err)
	}
}

func TestPresentationRegistryIsBoundedAndMissingStorageIsOptional(t *testing.T) {
	a := New(newTestContainer(t), nil)
	a.app.Feedback = nil
	var first string
	for i := 0; i <= maxPresentations; i++ {
		result := PlaylistResult{Tracks: []PlaylistTrack{{ID: "track"}}}
		a.preparePresentation(BuildPlaylistRequest{}, &result)
		if i == 0 {
			first = result.PresentationID
		}
		if err := a.AcknowledgePlaylistDisplayed(context.Background(), result.PresentationID); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.presentations.entries) != maxPresentations || a.presentations.entries[first] != nil {
		t.Fatal("unbounded presentation registry")
	}
	result := PlaylistResult{PresentationID: "stale"}
	a.preparePresentation(BuildPlaylistRequest{}, &result)
	if result.PresentationID != "" {
		t.Fatal("empty result can be exposed")
	}
}
