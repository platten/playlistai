package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// MusicKnowledge resolves extracted music terms only during explicit generation.
// Returned identities must map to the supplied catalog before retrieval.
type MusicKnowledge interface {
	ResolveMusic(context.Context, core.MusicIntent, Catalog, ReferenceResolver, Progress) (core.MusicIntent, error)
}

type GenreKnowledge interface {
	Graph(context.Context, []string) (core.GenreGraph, error)
}

// CachedGenreKnowledge must never issue a network request while typing.
type CachedGenreKnowledge interface {
	IsCachedGenre(context.Context, string) bool
}
