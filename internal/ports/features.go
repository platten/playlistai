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

// SemanticScorer evaluates an arbitrary candidate union with the same query
// encoder used for semantic retrieval. Absence from a top-K search is not
// treated as missing or mismatching evidence.
type SemanticScorer interface {
	Info() core.FeatureStoreInfo
	Score(ctx context.Context, text string, trackIDs []string) (core.QueryCoverage, []core.SemanticScore, error)
}
