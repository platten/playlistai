package musicbrainz

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestGenreDiscoveryWindowScalesButRemainsBounded(t *testing.T) {
	for _, tc := range []struct{ count, want int }{{0, 4}, {2, 4}, {10, 10}, {20, 20}, {100, 20}} {
		s := candidateStream{intent: core.MusicIntent{Controls: core.IntentControls{TotalTrackCount: tc.count}}}
		if got := s.discoveryWindowSize(); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
