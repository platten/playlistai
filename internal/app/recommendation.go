package app

import (
	"fmt"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
)

func (c *Container) RecommendationMode() core.RecommendationMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recommendationMode != "" && c.recommendationMode.Valid() {
		return c.recommendationMode
	}
	if c.cfg.Recommendation.Strategy == config.RecommendationDeejAI {
		return core.DeejAIOnly
	}
	return core.EnhancedHybrid
}

func (c *Container) SetRecommendationMode(mode core.RecommendationMode) error {
	if mode == "" || !mode.Valid() {
		return fmt.Errorf("unknown recommendation mode %q", mode)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.RecommendationMode = string(mode)
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return fmt.Errorf("save recommendation setting: %w", err)
	}
	c.recommendationMode = mode
	return nil
}
