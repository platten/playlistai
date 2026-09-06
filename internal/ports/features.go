package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

type FeatureStore interface {
	Info() core.FeatureStoreInfo
	Features(ctx context.Context, trackID string) (core.TrackFeatures, bool, error)
}

type SemanticSearcher interface {
	Info() core.FeatureStoreInfo
	Search(ctx context.Context, text string, limit int) ([]core.SemanticHit, error)
}
