package multichannel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type automaticBlockingEnricher struct{}

func (automaticBlockingEnricher) Name() string { return "test" }
func (automaticBlockingEnricher) Enrich(ctx context.Context, _ []core.TrackRef, _ ports.Progress) ([]core.EnrichedTrack, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestAutomaticOnlineTimeoutRetainsFrozenLocalEvidence(t *testing.T) {
	cat, intent, _ := automaticFixture(t, 1)
	automaticSupport(cat, intent, "0", "house")
	meta, _ := cat.Meta("0")
	engine := NewAutomatic(cat, nil, nil, DefaultConfig()).WithEnricher(automaticBlockingEnricher{})
	batch, err := engine.prepareBatch(context.Background(), cat, []core.TrackRef{meta.Ref}, intent)
	if err != nil {
		t.Fatal(err)
	}
	before := automaticCopy(batch.meta["0"])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := engine.acquireAutomaticEvidence(ctx, cat, batch, intent, "fixture", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(batch.ids) != 1 || batch.meta["0"].Ref != before.Ref || len(batch.meta["0"].Annotations) != len(before.Annotations) {
		t.Fatal("completed cached evidence lost")
	}
	if got := automaticAssessment(batch, "0", audio.Clauses(intent), .3); got.State != core.AutomaticStrong {
		t.Fatalf("completed fit changed: %+v", got)
	}
}
func TestAutomaticPreviewVectorRequiresExactDimensionsAndFiniteValues(t *testing.T) {
	record := core.AudioAnalysis{Model: core.AudioModelIdentity{Model: "test", Dimension: 2}, Segments: []core.AudioSegment{{Embedding: []float32{1, 0}}, {Embedding: []float32{0, 1}}}}
	got := automaticPreviewVector(record)
	if got.Source.SpaceID == "" || len(got.Values) != 2 || got.Values[0] != .5 || got.Values[1] != .5 {
		t.Fatal(got)
	}
	record.Segments[1].Embedding = []float32{1}
	if len(automaticPreviewVector(record).Values) != 0 {
		t.Fatal("incompatible segment admitted")
	}
}

type automaticPreviewFunc func(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error)

func (f automaticPreviewFunc) ResolveAudioPreview(ctx context.Context, ref core.TrackRef, recording core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	return f(ctx, ref, recording)
}

func TestAutomaticChecksCachedPreviewBeforeAcquiringEarlierMissingCandidate(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio")
	intent := testIntent(2)
	engine := NewAutomatic(cat, cat, nil, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	first, _ := cat.Meta("other")
	second, _ := cat.Meta("audio")
	batch, err := engine.prepareBatch(context.Background(), cat, []core.TrackRef{first.Ref, second.Ref}, intent)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	service.Resolver = automaticPreviewFunc(func(_ context.Context, ref core.TrackRef, _ core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
		called = true
		if ref.ID != "other" || batch.assessments["audio"].AnalysisID == "" {
			t.Fatal("new preview preceded cached evidence")
		}
		cancel()
		return core.ResolvedAudioPreview{}, context.Canceled
	})
	err = engine.acquireAutomaticEvidence(ctx, cat, batch, intent, cat.CatalogVersion(), nil)
	if !called || !errors.Is(err, context.Canceled) || batch.ids[0] != "other" || batch.assessments["audio"].AnalysisID == "" || len(batch.clap["audio"].Values) != 2 {
		t.Fatalf("cache/cancellation/order lost: %v %+v", err, batch.assessments)
	}
}

func TestAutomaticKeepsPreparedVectorOnPreviewAcquisition(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio")
	engine := NewAutomatic(cat, cat, nil, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	meta, _ := cat.Meta("audio")
	batch := newAutomaticBatch()
	batch.ids = []string{"audio"}
	batch.meta["audio"] = meta
	batch.clap["audio"] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "authenticated-prepared-pooling"}, Values: []float32{1, 0}}
	if err := engine.acquireAutomaticEvidence(context.Background(), cat, batch, testIntent(2), cat.CatalogVersion(), nil); err != nil {
		t.Fatal(err)
	}
	if batch.clap["audio"].Source.SpaceID != "authenticated-prepared-pooling" {
		t.Fatalf("compatible prepared vector replaced by unrelated preview space %s", batch.clap["audio"].Source.SpaceID)
	}
}
