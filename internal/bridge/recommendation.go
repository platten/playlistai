package bridge

import (
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
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
	if intent.Controls.RecommendationMode == core.Automatic {
		if engine, ok := a.runtime().AutomaticReco.(ports.VersionedRecommendationEngine); ok {
			return engine.AlgorithmVersion()
		}
		return "automatic-unavailable"
	}
	return a.recommendationVersion()
}
