package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

type AudioPreviewResolver interface {
	ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error)
}

// AudioAnalyzer borrows PCM for a call and must not retain it. Implementations
// release tensors before returning, including cancellation and failure paths.
type AudioAnalyzer interface {
	Identity() core.AudioModelIdentity
	EmbedAudio(context.Context, []float32) ([]float32, error)
	EmbedText(context.Context, string) ([]float32, error)
}

type AnalysisStore interface {
	Find(context.Context, string, string, string, core.AudioModelIdentity) (core.AudioAnalysis, bool, error)
	Put(context.Context, core.AudioAnalysis) error
	PutAssessment(context.Context, core.AudioAssessment) error
	Usage(context.Context) (core.AnalysisStorageUsage, error)
	Clear(context.Context) error
}

// CachedAnalysisScanner is an optional read-only retrieval capability. It
// streams bounded, validated analyses from one catalog and embedding space.
// The callback must not retain borrowed data or call back into the store.
type CachedAnalysisScanner interface {
	VisitAnalyses(context.Context, string, core.AudioModelIdentity, int, func(core.AudioAnalysis) bool) error
}

// CachedRecordingReader never performs HTTP requests. Generation uses this
// boundary; explicit enrichment may populate it in the background.
type CachedRecordingReader interface {
	CachedRecording(core.TrackRef) (core.EnrichedTrack, bool)
}
