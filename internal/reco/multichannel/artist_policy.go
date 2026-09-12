package multichannel

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// Suggestions explore artists within the eligible pool, including requests
// led by named references. Explicit artist/album-only restrictions opt out.
// Stored slider values and musical eligibility remain unchanged.
func genreArtistDiversity(intent core.MusicIntent) bool {
	// Enhanced respects the explicit diversity control through MMR and soft
	// spacing. A descriptive request does not create an adjacency prohibition.
	if intent.Controls.RecommendationMode == core.EnhancedHybrid {
		return false
	}
	for _, c := range intent.HardConstraints {
		if c.Kind == "require_artist" || c.Kind == "require_album" {
			return false
		}
	}
	if intent.VerificationPolicy == core.BestAvailable {
		return true
	}
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
