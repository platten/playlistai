package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// RecommendationEngine is the only component that turns a MusicIntent into an
// ordered playlist. Given (intent, catalog, seed) it is deterministic. It uses a
// Catalog to resolve seed queries and a SimilarityEngine for kNN; it makes no
// AI calls.
type RecommendationEngine interface {
	// Build resolves the intent's seeds against the catalog and walks the
	// embedding space to produce a playlist. If intent.Seeds resolves to
	// nothing it returns core.ErrNoSeeds.
	Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error)
}
