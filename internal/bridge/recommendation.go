package bridge

import (
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/reco/deejai"
)

// GetRecommendationMode returns the default for new generations. Saved requests
// pin their mode; Regenerate parses a new request with the current default.
func (a *API) GetRecommendationMode() core.RecommendationMode {
	return a.app.RecommendationMode()
}

func (a *API) SetRecommendationMode(mode core.RecommendationMode) error {
	return a.app.SetRecommendationMode(mode)
}

func (a *API) recommendationVersionFor(intent core.MusicIntent) string {
	if intent.Controls.RecommendationMode == core.DeejAIOnly {
		return deejai.OnlyAlgorithmVersion
	}
	return a.recommendationVersion()
}
