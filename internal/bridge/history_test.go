package bridge

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/history"
)

func TestDeriveTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		intent core.MusicIntent
		prompt string
		want   string
	}{
		{
			name:   "single seed",
			intent: core.MusicIntent{Seeds: core.IntentSeeds{Queries: []string{"Bonobo"}}, Mode: core.ModeSimilar},
			prompt: "something chill like bonobo",
			want:   "Like Bonobo",
		},
		{
			name:   "journey with two seeds",
			intent: core.MusicIntent{Seeds: core.IntentSeeds{Queries: []string{"Justice", "Kavinsky"}}, Mode: core.ModeJourney},
			prompt: "justice into kavinsky",
			want:   "Justice → Kavinsky",
		},
		{
			name:   "no seed falls back to prompt words",
			intent: core.MusicIntent{Mode: core.ModeSimilar},
			prompt: "  weekend wind-down set for sunday morning coffee time  ",
			want:   "Weekend wind-down set for sunday morning",
		},
		{
			name:   "no seed, empty prompt",
			intent: core.MusicIntent{},
			prompt: "   ",
			want:   "Untitled playlist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTitle(tc.intent, tc.prompt); got != tc.want {
				t.Fatalf("deriveTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadSavedPlaylistMigratesV2Request(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)
	record, err := c.History.Save(context.Background(), history.Record{
		Name: "Legacy", Prompt: "legacy prompt", Mode: "journey", TrackCount: 8,
		RequestJSON: []byte(`{"version":2,"referenceIds":["seed0001"],"requiredIds":["seed0003"],"mode":"journey","count":8,"creativity":0.7,"noise":0.2,"lookback":4,"noRepeatArtist":true}`),
		IntentJSON:  []byte(`{"version":2,"seeds":{"trackIds":["seed0001"]},"required":{"trackIds":["seed0003"]},"count":8,"mode":"journey"}`),
		TracksJSON:  []byte(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := api.LoadSavedPlaylist(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	intent := loaded.Request.Intent
	if loaded.Request.Version != core.CurrentIntentVersion || len(intent.References) != 1 || len(intent.RequiredTracks) != 1 {
		t.Fatalf("saved request not migrated: %+v", loaded.Request)
	}
	if intent.Controls.TotalTrackCount != 8 || intent.Controls.AudioWeight != 0.7 || intent.Controls.Discovery != 0.2 {
		t.Fatalf("saved controls not retained: %+v", intent.Controls)
	}
}

func TestSanitizeTitle(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`  "Midnight Drive"  `:      "Midnight Drive",
		"Sunday Coffee.\nblah blah": "Sunday Coffee",
		"“Rainy Day Jazz”":          "Rainy Day Jazz",
		"lots   of   spaces":        "lots of spaces",
		"Trailing punctuation!!!":   "Trailing punctuation",
		"":                          "",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}

	long := "a title that is quite a lot longer than forty eight characters for sure"
	got := sanitizeTitle(long)
	if len([]rune(got)) > 48 || !strings.HasSuffix(got, "…") {
		t.Errorf("sanitizeTitle(long) = %q; want <=48 runes ending in an ellipsis", got)
	}
}

func TestGenerateFromPromptSavesToHistory(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)

	if c.History == nil {
		t.Fatal("loaded container should have a history store")
	}

	before, err := api.ListSavedPlaylists()
	if err != nil {
		t.Fatalf("ListSavedPlaylists: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("fresh history should be empty, got %d", len(before))
	}

	if _, err := api.GenerateFromPrompt("like Justice, 10 tracks"); err != nil {
		t.Fatalf("GenerateFromPrompt: %v", err)
	}

	after, err := api.ListSavedPlaylists()
	if err != nil {
		t.Fatalf("ListSavedPlaylists: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("want 1 saved playlist, got %d", len(after))
	}
	rec := after[0]
	if rec.Prompt != "like Justice, 10 tracks" {
		t.Fatalf("saved prompt = %q", rec.Prompt)
	}
	if rec.TrackCount != 10 {
		t.Fatalf("saved trackCount = %d, want 10", rec.TrackCount)
	}
	if rec.Name == "" {
		t.Fatal("saved playlist must have a name")
	}
	if rec.CreatedAt == 0 {
		t.Fatal("saved playlist must have a createdAt")
	}

	// A failed generation must not leave a row behind.
	if _, err := api.GenerateFromPrompt("zqxjkw nothing here"); err == nil {
		t.Fatal("expected an error for an unresolvable prompt")
	}
	still, _ := api.ListSavedPlaylists()
	if len(still) != 1 {
		t.Fatalf("failed generation should not save; have %d rows", len(still))
	}

	if err := api.DeleteSavedPlaylist(rec.ID); err != nil {
		t.Fatalf("DeleteSavedPlaylist: %v", err)
	}
	gone, _ := api.ListSavedPlaylists()
	if len(gone) != 0 {
		t.Fatalf("row should be deleted, have %d", len(gone))
	}
}

func TestListSavedPlaylistsWithoutHistory(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)
	// newTestContainer has a data dir, so History is wired; just assert the
	// call is safe and returns an empty, non-nil slice.
	got, err := api.ListSavedPlaylists()
	if err != nil {
		t.Fatalf("ListSavedPlaylists: %v", err)
	}
	if got == nil {
		t.Fatal("must return a non-nil slice for JSON []")
	}
}
