package ports

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type audioContextReader struct {
	legacyCalls, contextCalls int
	cancel                    context.CancelFunc
}

func (r *audioContextReader) CachedRecording(ref core.TrackRef) (core.EnrichedTrack, bool) {
	r.legacyCalls++
	return core.EnrichedTrack{Ref: ref}, true
}

func (r *audioContextReader) CachedRecordingContext(ctx context.Context, ref core.TrackRef) (core.EnrichedTrack, bool) {
	r.contextCalls++
	if r.cancel != nil {
		r.cancel()
	}
	return core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionAmbiguous}, true
}

type audioLegacyReader struct{ calls int }

func (r *audioLegacyReader) CachedRecording(ref core.TrackRef) (core.EnrichedTrack, bool) {
	r.calls++
	return core.EnrichedTrack{Ref: ref}, true
}

func TestCachedRecordingContextPrefersCancellationAwareReader(t *testing.T) {
	ref := core.TrackRef{ID: "recording"}
	reader := &audioContextReader{}
	track, ok := CachedRecordingContext(context.Background(), reader, ref)
	if !ok || track.IdentityStatus != core.ResolutionAmbiguous || reader.contextCalls != 1 || reader.legacyCalls != 0 {
		t.Fatalf("context or explicit ambiguity lost: %+v %+v", track, reader)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader.cancel = cancel
	if track, ok := CachedRecordingContext(ctx, reader, ref); ok || track.Ref.ID != "" {
		t.Fatal("canceled partial identity escaped")
	}
	_, _ = CachedRecordingContext(ctx, reader, ref)
	if reader.contextCalls != 2 {
		t.Fatal("already canceled lookup dispatched")
	}
	legacy := &audioLegacyReader{}
	if track, ok := CachedRecordingContext(context.Background(), legacy, ref); !ok || track.Ref != ref || legacy.calls != 1 {
		t.Fatal("healthy legacy reader compatibility changed")
	}
}

type audioCatalogFixture struct{ Catalog }

func TestAudioMetadataCatalogIsRequestScoped(t *testing.T) {
	base := context.Background()
	a, b := &audioCatalogFixture{}, &audioCatalogFixture{}
	first := WithAudioMetadataCatalog(base, a)
	second := WithAudioMetadataCatalog(first, b)
	if _, ok := AudioMetadataCatalog(base); ok {
		t.Fatal("request catalog leaked to parent")
	}
	if got, ok := AudioMetadataCatalog(first); !ok || got != a {
		t.Fatal("first request catalog was mutated")
	}
	if got, ok := AudioMetadataCatalog(second); !ok || got != b {
		t.Fatal("nested request catalog unavailable")
	}
}
