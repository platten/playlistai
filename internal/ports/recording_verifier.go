package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// RecordingVerifier acquires cited facts only for a resolved recording.
// Implementations share the generation context, provider budgets and caches;
// unavailable evidence remains unknown and never becomes a successful claim.
type RecordingVerifier interface {
	VerifyRecording(context.Context, core.EnrichedTrack, []core.MusicalCriterion) (core.EnrichedTrack, error)
}

// CachedRecordingVerifier rechecks known identity evidence without network or
// source extraction. A cache miss leaves identity unchanged; a demonstrated
// same-recording conflict must remain ambiguous even outside an online quota.
type CachedRecordingVerifier interface {
	VerifyCachedRecording(context.Context, core.EnrichedTrack) (core.EnrichedTrack, error)
}

// RecordingSource contains bounded public text from an identity-linked source.
// It is untrusted data; extraction cannot run tools or change listener intent.
type RecordingSource struct {
	Track  core.EnrichedTrack
	Source core.ContextSource
	Text   string
}

type RecordingSourceExtractor interface {
	ExtractRecordingClaims(context.Context, RecordingSource) ([]core.RecordingClaim, error)
}
