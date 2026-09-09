package multichannel

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// Genre playlists explore artists within the eligible musical category. This
// engine policy leaves explicit artist-only requests and stored slider values
// unchanged; it never widens musical eligibility to manufacture diversity.
func genreArtistDiversity(intent core.MusicIntent) bool {
	for _, p := range intent.Preferences.Genres {
		if p.Influence != core.InfluenceNegative && strings.TrimSpace(p.Value) != "" {
			return true
		}
	}
	for _, c := range intent.EssentialCriteria {
		if (c.Kind == "genre" || c.Kind == "style") && strings.TrimSpace(c.Value) != "" {
			return true
		}
	}
	for _, c := range intent.HardConstraints {
		if c.Kind == "require_style" && strings.TrimSpace(c.Value) != "" {
			return true
		}
	}
	return false
}
