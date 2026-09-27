package multichannel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type cancelingChannelCatalog struct {
	ports.Catalog
	calls       int
	cancel      context.CancelFunc
	cancelAfter int
}

func (c *cancelingChannelCatalog) Meta(id string) (core.TrackMeta, bool) {
	c.calls++
	if c.calls == max(1, c.cancelAfter) && c.cancel != nil {
		c.cancel()
	}
	return core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: "Artist", Title: "Song"}}, true
}

type interruptedChannelSearch struct {
	ports.SimilarityEngine
	cancel context.CancelFunc
	err    error
}

func (interruptedChannelSearch) Len() int { return enhancedChannelBatch }

func (s interruptedChannelSearch) Search(context.Context, ports.SimilarityQuery) ([]ports.Match, error) {
	if s.cancel != nil {
		s.cancel()
	}
	out := make([]ports.Match, enhancedChannelBatch)
	for i := range out {
		out[i] = ports.Match{ID: fmt.Sprintf("fixture:%d", i), Score: 1}
	}
	return out, s.err
}

func TestSeedChannelStopsMetadataAfterCancellation(t *testing.T) {
	for _, cancelInSearch := range []bool{false, true} {
		name := "after first metadata read"
		if cancelInSearch {
			name = "as search returns"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cat := &cancelingChannelCatalog{cancel: cancel}
			search := interruptedChannelSearch{}
			if cancelInSearch {
				search.cancel = cancel
			}
			r := NewRetriever(cat, search, Config{})
			byID := map[string]*core.Candidate{}
			var exploration []explorationOption
			err := r.searchChannel(ctx, byID, &exploration, nil, ChannelSeedAudio, "reference", []float32{1}, nil, [2]float32{1, 0}, 1, enhancedChannelBatch, 0)
			want := 1
			if cancelInSearch {
				want = 0
			}
			if cat.calls != want || len(byID) != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("metadata calls=%d candidates=%d error=%v; want %d reads, no post-cancel result and cancellation", cat.calls, len(byID), err, want)
			}
		})
	}
}

type blockedContextMetadata struct {
	*fakes.Catalog
	blockID     string
	entered     chan struct{}
	once        sync.Once
	legacyCalls atomic.Int32
}

func (c *blockedContextMetadata) Meta(id string) (core.TrackMeta, bool) {
	c.legacyCalls.Add(1)
	return c.Catalog.Meta(id)
}

func (c *blockedContextMetadata) MetaContext(ctx context.Context, id string) (core.TrackMeta, bool) {
	if id == c.blockID {
		c.once.Do(func() { close(c.entered) })
		<-ctx.Done()
		return core.TrackMeta{}, false
	}
	return c.Catalog.Meta(id)
}

func TestReferenceMetadataCancellationReachesCatalog(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		for _, stage := range []string{"reference", "required fallback", "context seed", "candidate", "ranker reference", "ranker exposure"} {
			if stage == "context seed" && mode != core.EnhancedHybrid {
				continue
			}
			t.Run(string(mode)+"/"+stage, func(t *testing.T) {
				intent := contextIntent()
				intent.Controls.RecommendationMode = mode
				cat := &blockedContextMetadata{Catalog: testCatalog(), blockID: "seed", entered: make(chan struct{})}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var profile core.TasteProfile
				switch stage {
				case "required fallback":
					intent.References, intent.Knowledge = nil, nil
					intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed"}}
				case "context seed":
					cat.blockID = "other"
				case "candidate":
					intent.Knowledge = nil
					cat.blockID = "audio"
				case "ranker exposure":
					intent.References, intent.Knowledge = nil, nil
					profile.RecentExposures = map[string]float64{"seed": 1}
				}
				done := make(chan error, 1)
				go func() {
					var err error
					if stage == "ranker reference" || stage == "ranker exposure" {
						_, err = NewRanker(cat, DefaultConfig()).Rank(ctx, nil, ports.RankRequest{Intent: intent, Profile: profile})
					} else {
						_, err = NewRetriever(cat, fakes.NewSimilarityEngine(cat.Catalog), DefaultConfig()).Retrieve(ctx, ports.RetrievalRequest{Intent: intent})
					}
					done <- err
				}()
				select {
				case <-cat.entered:
				case err := <-done:
					t.Fatalf("context metadata was bypassed: error=%v legacy reads=%d", err, cat.legacyCalls.Load())
				case <-time.After(time.Second):
					cancel()
					<-done
					t.Fatal("context metadata was not reached")
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) || cat.legacyCalls.Load() != 0 {
						t.Fatalf("cancellation lost: error=%v legacy reads=%d", err, cat.legacyCalls.Load())
					}
				case <-time.After(time.Second):
					t.Fatal("catalog context cancellation did not interrupt the caller")
				}
			})
		}
	}
}

func TestContextMetadataPreservesReferenceRetrieval(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		t.Run(string(mode), func(t *testing.T) {
			base := testCatalog()
			cat := &blockedContextMetadata{Catalog: base}
			intent := contextIntent()
			intent.Controls.RecommendationMode = mode
			request := ports.RetrievalRequest{Intent: intent, Seed: 42}
			want, err := NewRetriever(base, fakes.NewSimilarityEngine(base), DefaultConfig()).Retrieve(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewRetriever(cat, fakes.NewSimilarityEngine(base), DefaultConfig()).Retrieve(context.Background(), request)
			if err != nil || !reflect.DeepEqual(got, want) || cat.legacyCalls.Load() != 0 {
				t.Fatalf("context metadata changed reference results or bypassed context: error=%v legacy=%d\ngot=%+v\nwant=%+v", err, cat.legacyCalls.Load(), got, want)
			}
		})
	}
}

func TestSeedChannelKeepsPartialMatchesOnSourceFailure(t *testing.T) {
	failure := errors.New("source interrupted")
	cat := &cancelingChannelCatalog{}
	r := NewRetriever(cat, interruptedChannelSearch{err: failure}, Config{})
	byID := map[string]*core.Candidate{}
	var exploration []explorationOption
	err := r.searchChannel(context.Background(), byID, &exploration, nil, ChannelSeedAudio, "reference", []float32{1}, nil, [2]float32{1, 0}, 1, enhancedChannelBatch, 0)
	if cat.calls != enhancedChannelBatch || len(byID) != enhancedChannelBatch || !errors.Is(err, failure) {
		t.Fatalf("partial source result lost: calls=%d candidates=%d error=%v", cat.calls, len(byID), err)
	}
}

func TestSeedChannelKeepsMetadataCompletedBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cat := &cancelingChannelCatalog{cancel: cancel, cancelAfter: 2}
	r := NewRetriever(cat, interruptedChannelSearch{}, Config{})
	byID := map[string]*core.Candidate{}
	var exploration []explorationOption
	err := r.searchChannel(ctx, byID, &exploration, nil, ChannelSeedAudio, "reference", []float32{1}, nil, [2]float32{1, 0}, 1, enhancedChannelBatch, 0)
	if cat.calls != 2 || len(byID) != 1 || byID["fixture:0"] == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel metadata lost: calls=%d candidates=%v error=%v", cat.calls, byID, err)
	}
}

type blockingKnowledgeBinder struct {
	*fakes.Catalog
	entered chan struct{}
	legacy  atomic.Int32
}

func (b *blockingKnowledgeBinder) BindRecordingKnowledgeContext(ctx context.Context, _ []core.EnrichedTrack) {
	close(b.entered)
	<-ctx.Done()
}

func (b *blockingKnowledgeBinder) BindRecordingKnowledge([]core.EnrichedTrack) {
	b.legacy.Add(1)
}

func TestRecommendationKnowledgeBindingUsesRequestContext(t *testing.T) {
	base := testCatalog()
	cat := &blockingKnowledgeBinder{Catalog: base, entered: make(chan struct{})}
	engine := New(cat, fakes.NewSimilarityEngine(base), base, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: contextIntent()})
		done <- err
	}()
	select {
	case <-cat.entered:
	case err := <-done:
		t.Fatalf("contextual binder bypassed: error=%v legacy calls=%d", err, cat.legacy.Load())
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("contextual binder not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || cat.legacy.Load() != 0 {
			t.Fatalf("binder cancellation lost: error=%v legacy calls=%d", err, cat.legacy.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("binder did not receive generation cancellation")
	}
}
