package multichannel

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type blockingDiscovery struct {
	fixtureDiscovery
	waiting chan struct{}
}

func (s *blockingDiscovery) OpenCandidates(core.MusicIntent, ports.Catalog, ports.ReferenceResolver) ports.MusicCandidateStream {
	return s
}
func (s *blockingDiscovery) Next(ctx context.Context) (core.TrackRef, error) {
	if s.pulls == 0 {
		return s.fixtureDiscovery.Next(ctx)
	}
	close(s.waiting)
	<-ctx.Done()
	return core.TrackRef{}, ctx.Err()
}

func TestStopInterruptsBlockedDiscoveryAndKeepsAcceptedTracks(t *testing.T) {
	cat := testCatalog()
	stream := &blockingDiscovery{fixtureDiscovery: fixtureDiscovery{tracks: refs(cat, "audio")}, waiting: make(chan struct{})}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(stream)
	stop := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	var result core.Playlist
	var err error
	go func() {
		defer close(done)
		result, err = engine.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: testIntent(2), StopChecking: stop})
	}()
	select {
	case <-stream.waiting:
	case <-ctx.Done():
		t.Fatal("discovery did not start")
	}
	close(stop)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("stop failed to interrupt discovery")
	}
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "audio" {
		t.Fatalf("checked tracks lost: %+v %v", result.Tracks, err)
	}
}

type forbiddenRetriever struct{ t *testing.T }

func (r forbiddenRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	r.t.Error("eager retrieval despite sufficient discovery")
	return nil, nil
}

func TestDiscoveryStopsWithoutSpeculativeRankingOrEagerRetrieval(t *testing.T) {
	var tracks []fakes.CatalogTrack
	for i, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: id + " - Song", Audio: []float32{float32(i + 1), 1}, Track: []float32{1, 0}})
	}
	cat := fakes.NewCatalog(2, tracks...)
	source := &fixtureDiscovery{tracks: refs(cat, "a", "b", "c", "d", "e", "f", "g", "h", "i")}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source)
	engine.retriever = forbiddenRetriever{t}
	intent := testIntent(1)
	intent.References = nil
	intent.Seeds = core.IntentSeeds{}
	// Historical taste must not cause extra discovery once a valid playlist
	// meets the count. Rank the accepted set, not speculative unseen tracks.
	profile := core.TasteProfile{RequestPositive: core.EmbeddingAffinity{Audio: []float32{9, 1}, Cooccurrence: []float32{1, 0}}}
	result, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, Profile: profile})
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "a" || source.pulls != 1 {
		t.Fatalf("discovery continued past requested count: %+v pulls=%d err=%v", result.Tracks, source.pulls, err)
	}
}
