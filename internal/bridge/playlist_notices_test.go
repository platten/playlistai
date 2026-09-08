package bridge

import (
	"io"
	"log/slog"
	"strings"
	"testing"

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
