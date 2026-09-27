package multichannel

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

func TestAutomaticMetadataOrderKeepsRequiredRanksTiesAndFallback(t *testing.T) {
	b := durationBatchFixture(6)
	a := &AutomaticEngine{cfg: DefaultConfig()}
	candidates := []core.Candidate{{Track: b.meta[b.ids[4]].Ref, Sources: []core.RetrievalEvidence{{Rank: 1}}}, {Track: b.meta[b.ids[3]].Ref, Sources: []core.RetrievalEvidence{{Rank: 2}}}, {Track: b.meta[b.ids[2]].Ref, Sources: []core.RetrievalEvidence{{Rank: 2}}}}
	before := automaticCopy(candidates)
	order := a.automaticMetadataOrder(b, candidates, []core.TrackRef{b.meta[b.ids[5]].Ref}, 42)
	if order[0] != b.ids[5] || order[1] != b.ids[4] || !reflect.DeepEqual(order[4:], b.ids[:2]) {
		t.Fatal(order)
	}
	if !reflect.DeepEqual(order, a.automaticMetadataOrder(b, candidates, []core.TrackRef{b.meta[b.ids[5]].Ref}, 42)) || !reflect.DeepEqual(candidates, before) {
		t.Fatal("unstable or mutated order")
	}
	if stableHash(order[2], 42) > stableHash(order[3], 42) {
		t.Fatal("seed tie ignored")
	}
}

func TestAutomaticPrioritizesLateCandidateBeforeMissingPreview(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio")
	b := durationBatchFixture(64)
	late := b.ids[63]
	for _, id := range []string{"other", "audio"} {
		m, _ := cat.Meta(id)
		b.ids = append(b.ids, id)
		b.meta[id] = m
	}
	var candidates []core.Candidate
	for i, id := range b.ids[:64] {
		rank := 100 + i
		if id == late {
			rank = 1
		}
		candidates = append(candidates, core.Candidate{Track: b.meta[id].Ref, Sources: []core.RetrievalEvidence{{Rank: rank}}})
	}
	f := &durationVerifierFixture{update: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		if row.Ref.ID == late {
			row.IdentityStatus = core.ResolutionAmbiguous
		}
		return row, nil
	}}
	a := NewAutomatic(cat, cat, nil, DefaultConfig()).WithEnricher(f).WithAudioProvider(func() *audio.Service { return service })
	order := a.automaticMetadataOrder(b, candidates, nil, 42)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	service.Resolver = automaticPreviewFunc(func(_ context.Context, _ core.TrackRef, _ core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
		called = true
		if f.calls != 32 || b.recordings[late].IdentityStatus != core.ResolutionAmbiguous || b.assessments["audio"].AnalysisID == "" {
			t.Fatalf("preview preceded ranked metadata/cache: calls=%d late=%+v cached=%+v", f.calls, b.recordings[late], b.assessments["audio"])
		}
		cancel()
		return core.ResolvedAudioPreview{}, context.Canceled
	})
	err := a.acquireAutomaticEvidence(ctx, cat, b, testIntent(2), cat.CatalogVersion(), order)
	if !called || !errors.Is(err, context.Canceled) || automaticFacts(b, b.meta[late].Ref, core.MusicIntent{}, "playlist") {
		t.Fatal("priority conflict lost", err)
	}
}

func TestAutomaticCachedPreviewSurvivesBlockingMetadata(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio")
	b := durationBatchFixture(1)
	m, _ := cat.Meta("audio")
	b.ids = append(b.ids, "audio")
	b.meta["audio"] = m
	b.clap["audio"] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "prepared"}, Values: []float32{1, 0}}
	ready := make(chan bool, 1)
	f := &durationVerifierFixture{update: func(ctx context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		ready <- b.assessments["audio"].AnalysisID != ""
		<-ctx.Done()
		return row, ctx.Err()
	}}
	a := NewAutomatic(cat, cat, nil, DefaultConfig()).WithEnricher(f).WithAudioProvider(func() *audio.Service { return service })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.acquireAutomaticEvidence(ctx, cat, b, testIntent(2), cat.CatalogVersion(), nil) }()
	select {
	case cached := <-ready:
		if !cached {
			t.Fatal("metadata preceded cached assessment")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("metadata verifier was not reached")
	}
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled metadata verification did not stop")
	}
	if !errors.Is(err, context.Canceled) || f.calls != 1 || b.assessments["audio"].AnalysisID == "" || b.clap["audio"].Source.SpaceID != "prepared" {
		t.Fatal("cache/vector lost on stop", err)
	}
}
