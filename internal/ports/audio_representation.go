package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// AudioRepresentationAnalyzer borrows model-preprocessed PCM for one call. It
// must not retain PCM and must release tensors on success, failure or cancellation.
// Its output is an owned normalized segment vector, not text-aligned evidence.
type AudioRepresentationAnalyzer interface {
	Identity() core.AudioRepresentationIdentity
	EmbedAudio(context.Context, []float32) ([]float32, error)
}

// AudioRepresentationStore is independent of the paired CLAP analysis store.
// Find is cache-only and returns false for missing/incompatible identities.
// Put is idempotent; Clear removes only audio-only representations.
type AudioRepresentationStore interface {
	Find(context.Context, string, string, string, core.AudioRepresentationIdentity) (core.AudioRepresentation, bool, error)
	Put(context.Context, core.AudioRepresentation) error
	Usage(context.Context) (core.AudioRepresentationStorageUsage, error)
	Clear(context.Context) error
}

// AudioRepresentationQuery searches one fully identified audio-only space.
// Exclude contains catalog track IDs. Limit zero requests coverage/fingerprint
// only and permits an empty Vector; it never returns neighbors.
type AudioRepresentationQuery struct {
	CatalogVersion string
	Model          core.AudioRepresentationIdentity
	Vector         []float32
	Limit          int
	Exclude        map[string]struct{}
}

// AudioRepresentationSearcher is separate from the point-lookup store so
// existing analyzers and fakes need not implement vector retrieval. A call reads
// a stable view; callers freeze its hits when a generation uses several refills.
type AudioRepresentationSearcher interface {
	Search(context.Context, AudioRepresentationQuery) (core.AudioRepresentationSearchResult, error)
}

// AudioRepresentationBatchSearcher evaluates a bounded set of queries against
// one compatible read view. Every result has the same coverage and fingerprint.
// Queries must share catalog/model identity; exclusion sets may differ.
type AudioRepresentationBatchSearcher interface {
	SearchBatch(context.Context, []AudioRepresentationQuery) ([]core.AudioRepresentationSearchResult, error)
}
