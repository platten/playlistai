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
