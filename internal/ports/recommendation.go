package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// RecommendationEngine turns a MusicIntent into an ordered playlist without
// making language-model calls. Implementations may be the versioned Deej-AI
// baseline or an orchestrator that separates retrieval, hard eligibility,
// ranking, diversity selection, and sequencing.
type RecommendationEngine interface {
	// Build resolves reference and required tracks and builds a deterministic
	// result for the intent's seed. If no supported retrieval channel is
	// available it returns core.ErrNoSeeds. A valid result may be partial;
	// Playlist.Notices explains exhausted eligibility.
	Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error)
}

// PersonalizedRecommendationEngine accepts the deterministic profile snapshot
// recorded in generation metadata. The legacy RecommendationEngine remains the
// compatibility and evaluation baseline.
type PersonalizedRecommendationEngine interface {
	BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error)
}

// RecommendationRequest carries generation context that affects results but
// is not part of the user's musical intent. RecentSelections is used when
// continuing radio; the original Intent references remain the primary anchor.
type RecommendationRequest struct {
	Intent           core.MusicIntent
	Profile          core.TasteProfile
	RecentSelections []core.TrackRef
}

type ContextualRecommendationEngine interface {
	BuildRecommendation(ctx context.Context, request RecommendationRequest) (core.Playlist, error)
}

type VersionedRecommendationEngine interface {
	AlgorithmVersion() string
}

type RetrievalRequest struct {
	Intent           core.MusicIntent
	Profile          core.TasteProfile
	RecentSelections []core.TrackRef
	Seed             int64
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

type SelectionRequest struct {
	Intent           core.MusicIntent
	Required         []core.TrackRef
	Waypoints        []core.TrackRef
	RecentSelections []core.TrackRef
	Count            int
}

type SelectionResult struct {
	Candidates []core.Candidate
	Notices    []core.PlaylistNotice
}

type CandidateSelector interface {
	Select(ctx context.Context, candidates []core.Candidate, request SelectionRequest) (SelectionResult, error)
}

// Trajectory supplies embedding-space targets for ordering. Implementations
// must report their evidence; an energy trajectory remains unsupported until
// the catalog has an actual acoustic energy feature.
type Trajectory interface {
	Target(position float64) (Vectors, bool)
	Evidence() string
}

type SequenceRequest struct {
	Intent           core.MusicIntent
	Candidates       []core.Candidate
	Required         []core.TrackRef
	Waypoints        []core.TrackRef
	ReferenceAnchors []core.TrackRef
	RecentSelections []core.TrackRef
	Trajectory       Trajectory
	Seed             int64
}

type SequenceResult struct {
	Tracks    []core.TrackRef
	Rationale []core.StepReason
	Notices   []core.PlaylistNotice
}

type PlaylistSequencer interface {
	Sequence(ctx context.Context, request SequenceRequest) (SequenceResult, error)
}
