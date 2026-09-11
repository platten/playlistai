package bridge

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
)

func TestPlaylistDiagnosticsGoToSessionLogs(t *testing.T) {
	store := &logging.Store{}
	api := New(nil, slog.New(logging.NewHandler(slog.NewTextHandler(io.Discard, nil), store)))
	notices := []PlaylistNotice{
		{Code: "inferred_anchor_rejected", Detail: "Inferred anchor Example was rejected"},
		{Code: "semantic_constraints_enforced", Detail: "2 grounded semantic hard constraints were enforced", Requested: 20, Actual: 60},
		{Code: "semantic_fallback", Detail: "seeded embedding retrieval remained active", Requested: 20, Actual: 60},
		{Code: "eligible_tracks_exhausted", Detail: "Only 10 matching tracks were found", Requested: 20, Actual: 10},
		{Code: "music_lookup_0", Detail: "Artist was absent from the local catalog"},
	}
	result := PlaylistResult{GenerationID: "notice-test", Notices: notices, Status: GenerationStatus{PartialReasons: notices}}
	api.presentPlaylistNotices(&result)
	for _, presented := range [][]PlaylistNotice{result.Notices, result.Status.PartialReasons} {
		if len(presented) != 3 || presented[0].Code != "semantic_fallback" || presented[0].Requested != 0 || presented[1] != notices[3] || presented[2] != notices[4] {
			t.Fatalf("unexpected presented notices: %+v", presented)
		}
		if strings.Contains(presented[0].Detail, "embedding") {
			t.Fatal("technical diagnostic leaked into playlist")
		}
	}
	entries := store.Read(0)
	if len(entries) != 3 {
		t.Fatalf("expected each diagnostic once, got %+v", entries)
	}
	for i, entry := range entries {
		if !strings.Contains(entry.Text, notices[i].Detail) || !strings.Contains(entry.Text, "generation_id=notice-test") {
			t.Fatalf("diagnostic or generation missing from log: %s", entry.Text)
		}
	}
}

type partialOutcomeEngine struct {
	actual int
	state  core.GenerationOutcomeState
}

func (e partialOutcomeEngine) Build(_ context.Context, intent core.MusicIntent) (core.Playlist, error) {
	tracks := []core.TrackRef{{ID: "seed0001", Artist: "Fixture", Title: "First"}, {ID: "seed0002", Artist: "Fixture", Title: "Second"}}
	return core.Playlist{Intent: intent, Seed: intent.Seed, Tracks: tracks[:e.actual], Outcome: core.GenerationOutcome{
		State: e.state, Reasons: []core.OutcomeReason{{Code: "suggested_musical_fit", Detail: "Some characteristics could not be verified."}},
	}}, nil
}

func TestPartialNoticeSeparatesTrackCountFromFulfillment(t *testing.T) {
	for _, tt := range []struct {
		name   string
		actual int
		state  core.GenerationOutcomeState
		code   string
	}{
		{"full count with unverified fit", 2, core.OutcomePartial, "partial_request_fulfillment"},
		{"too few tracks", 1, core.OutcomePartial, "partial_result"},
		{"no tracks", 0, core.OutcomePartial, "partial_result"},
		{"fulfilled", 2, core.OutcomeFulfilled, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newLoadedContainer(t)
			api := New(c, nil)
			useRecommendationEngine(api, partialOutcomeEngine{actual: tt.actual, state: tt.state})
			result, err := api.BuildPlaylist(context.Background(), BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: core.MusicIntent{
				Version: core.CurrentIntentVersion, References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed0001", Influence: core.InfluencePositive}},
				Controls: core.IntentControls{TotalTrackCount: 2, AudioWeight: .5, CooccurrenceWeight: .5}, Seed: "7",
			}})
			if err != nil || result.Outcome.State != tt.state || result.Status.State != string(tt.state) || len(result.Tracks) != tt.actual {
				t.Fatalf("outcome changed: %+v %v", result, err)
			}
			if tt.code == "" {
				if len(result.Status.PartialReasons) != 0 || len(result.Notices) != 0 {
					t.Fatal("fulfilled result acquired a partial warning")
				}
				return
			}
			if len(result.Notices) != 1 || len(result.Status.PartialReasons) != 1 || result.Notices[0].Code != tt.code || result.Notices[0] != result.Status.PartialReasons[0] {
				t.Fatalf("wrong partial notices: %+v", result)
			}
			notice := result.Notices[0]
			if tt.actual == 2 {
				if strings.Contains(notice.Detail, "total") || notice.Requested != 0 || notice.Actual != 0 || len(result.Outcome.Reasons) != 1 {
					t.Fatal("full-count uncertainty misreported as missing tracks")
				}
			} else if notice.Requested != 2 || notice.Actual != tt.actual {
				t.Fatal("shortfall counts lost")
			}
		})
	}
}

func TestSavedPartialNoticePresentationRepairsFalseShortfall(t *testing.T) {
	api := New(nil, nil)
	for _, actual := range []int{1, 2} {
		original := PlaylistNotice{Code: "partial_result", Detail: "generation ended before the requested total was reached", Requested: 2, Actual: actual}
		result := PlaylistResult{
			Intent: core.MusicIntent{Controls: core.IntentControls{TotalTrackCount: 2}}, Tracks: make([]PlaylistTrack, actual),
			Outcome: core.GenerationOutcome{State: core.OutcomePartial}, Notices: []PlaylistNotice{original}, Status: GenerationStatus{State: "partial", PartialReasons: []PlaylistNotice{original}},
		}
		api.presentPlaylistNotices(&result)
		if result.Outcome.State != core.OutcomePartial || result.Status.State != "partial" || result.Notices[0] != result.Status.PartialReasons[0] {
			t.Fatal("presentation changed fulfillment or lost consistent notices")
		}
		if actual == 2 && result.Notices[0].Code != "partial_request_fulfillment" {
			t.Fatal("saved full-length result still claims a shortfall")
		}
		if actual < 2 && result.Notices[0] != original {
			t.Fatal("actual saved shortfall was concealed")
		}
	}
}
