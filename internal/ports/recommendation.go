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
	// Build resolves reference and required tracks, then walks the embedding
	// space. If neither set resolves it returns core.ErrNoSeeds. A valid result
	// may be partial; Playlist.Notices explains exhausted eligibility.
	Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error)
}

// PersonalizedRecommendationEngine accepts the deterministic profile snapshot
// recorded in generation metadata. The legacy RecommendationEngine remains the
// compatibility and evaluation baseline.
type PersonalizedRecommendationEngine interface {
	BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error)
}

type VersionedRecommendationEngine interface {
	AlgorithmVersion() string
}

type RetrievalRequest struct {
	Intent  core.MusicIntent
	Profile core.TasteProfile
	Seed    int64
}

type CandidateRetriever interface {
	Retrieve(ctx context.Context, request RetrievalRequest) ([]core.Candidate, error)
}

type RankRequest struct {
	Intent  core.MusicIntent
	Profile core.TasteProfile
}

type Ranker interface {
	Rank(ctx context.Context, candidates []core.Candidate, request RankRequest) ([]core.Candidate, error)
}
