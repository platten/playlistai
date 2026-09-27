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
// streams validated analyses from one catalog and embedding space in bounded
// pages. A negative limit scans to exhaustion; zero performs no reads.
// The callback must not retain borrowed data or call back into the store.
type CachedAnalysisScanner interface {
	VisitAnalyses(context.Context, string, core.AudioModelIdentity, int, func(core.AudioAnalysis) bool) error
}

// CachedRecordingReader never performs HTTP requests. Generation uses this
// boundary; explicit enrichment may populate it in the background.
type CachedRecordingReader interface {
	CachedRecording(core.TrackRef) (core.EnrichedTrack, bool)
}

// ContextCachedRecordingReader lets local metadata lookups honor generation
// cancellation. Like CachedRecordingReader, it never performs HTTP requests.
type ContextCachedRecordingReader interface {
	CachedRecordingContext(context.Context, core.TrackRef) (core.EnrichedTrack, bool)
}

func CachedRecordingContext(ctx context.Context, reader CachedRecordingReader, ref core.TrackRef) (core.EnrichedTrack, bool) {
	if reader == nil || ctx.Err() != nil {
		return core.EnrichedTrack{}, false
	}
	var track core.EnrichedTrack
	var ok bool
	if contextual, supported := reader.(ContextCachedRecordingReader); supported {
		track, ok = contextual.CachedRecordingContext(ctx, ref)
	} else {
		track, ok = reader.CachedRecording(ref)
	}
	if ctx.Err() != nil {
		return core.EnrichedTrack{}, false
	}
	return track, ok
}

type audioMetadataCatalogKey struct{}

// WithAudioMetadataCatalog pins the request's composed catalog for preview
// identity checks without changing shared catalog state.
func WithAudioMetadataCatalog(ctx context.Context, catalog Catalog) context.Context {
	if catalog == nil {
		return ctx
	}
	return context.WithValue(ctx, audioMetadataCatalogKey{}, catalog)
}

func AudioMetadataCatalog(ctx context.Context) (Catalog, bool) {
	catalog, ok := ctx.Value(audioMetadataCatalogKey{}).(Catalog)
	return catalog, ok && catalog != nil
}
