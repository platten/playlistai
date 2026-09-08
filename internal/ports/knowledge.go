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

// IterativeMusicKnowledge prepares identity/artist pools without eagerly
// consuming the generation budget on a fixed recording batch.
type IterativeMusicKnowledge interface {
	PrepareMusic(context.Context, core.MusicIntent, Catalog, ReferenceResolver, Progress) (core.MusicIntent, error)
}

// MusicCandidateSource opens a request-local, bounded stream of real catalog
// recordings. Artist tags steer discovery, never establish track eligibility.
type MusicCandidateSource interface {
	OpenCandidates(core.MusicIntent, Catalog, ReferenceResolver) MusicCandidateStream
}

type MusicCandidateStream interface {
	Next(context.Context) (core.TrackRef, error) // io.EOF means this pool is exhausted
	Snapshot() *core.KnowledgeSnapshot
}

// ArtistRecordingCatalog avoids a full fuzzy catalog scan for every recording
// on an artist's provider page. artist is a resolved catalog spelling.
type ArtistRecordingCatalog interface {
	ArtistRecordings(context.Context, string) ([]core.TrackRef, error)
}

type GenreKnowledge interface {
	Graph(context.Context, []string) (core.GenreGraph, error)
}

// GenreNameKnowledge identifies categories without fetching relationships or
// recordings. It may access the network only for an explicitly submitted job.
type GenreNameKnowledge interface {
	GenreNames(context.Context) (core.GenreGraph, error)
}

// CachedGenreKnowledge must never issue a network request while typing.
type CachedGenreKnowledge interface {
	IsCachedGenre(context.Context, string) bool
}
