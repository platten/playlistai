package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

type FeatureStore interface {
	Info() core.FeatureStoreInfo
	Features(ctx context.Context, trackID string) (core.TrackFeatures, bool, error)
}

// TextEmbedder and SemanticSearcher must use the same model and revision.
// SemanticSearcher implementations reject incompatible encoders.
type TextEmbedder interface {
	Model() (name, revision string, dimension int)
	Embed(ctx context.Context, text string) ([]float32, error)
}

type SemanticSearcher interface {
	Info() core.FeatureStoreInfo
	Search(ctx context.Context, text string, limit int) ([]core.SemanticHit, error)
}
