package multichannel

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type discoveryProfileFixture struct {
	ports.Catalog
	profiles []core.DiscoveryProfile
	calls    int
	onRead   func(context.Context) error
}

func (c *discoveryProfileFixture) DiscoveryProfiles(ctx context.Context, _ core.MusicIntent, _ int) ([]core.DiscoveryProfile, error) {
	c.calls++
	if c.onRead != nil {
		return nil, c.onRead(ctx)
	}
	return c.profiles, nil
}

type discoveryIntentRecorder struct {
	seen      []core.MusicIntent
	snapshot  *core.KnowledgeSnapshot
	err       error
	replayErr error
	nextCalls int
}

func (s *discoveryIntentRecorder) OpenCandidates(intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver) ports.MusicCandidateStream {
	s.seen = append(s.seen, intent)
	s.snapshot = intent.Knowledge
	return s
}
func (s *discoveryIntentRecorder) Next(context.Context) (core.TrackRef, error) {
	s.nextCalls++
	if s.err != nil {
		return core.TrackRef{}, s.err
	}
	return core.TrackRef{}, io.EOF
}
func (s *discoveryIntentRecorder) Snapshot() *core.KnowledgeSnapshot { return s.snapshot }

func (s *discoveryIntentRecorder) ReplayError() error { return s.replayErr }

func TestDiscoveryOverlayEvidenceIsRequestScoped(t *testing.T) {
	base := testCatalog()
	cat := &cancellableLibraryFixture{Catalog: base, reads: map[string]int{}}
	engine := New(base, fakes.NewSimilarityEngine(base), base, DefaultConfig())
	enabled := true
	var modes []core.RecommendationMode
	releases := 0
	engine.WithIntentOverlayProvider(func(_ context.Context, intent core.MusicIntent, _ ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (RequestOverlay, error) {
		modes = append(modes, intent.Controls.RecommendationMode)
		return RequestOverlay{Catalog: cat, Resolver: resolver, Retriever: retriever, EnableLibraryEvidence: enabled, Release: func() { releases++ }}, nil
	})
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	if _, err := engine.Build(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if len(cat.reads) == 0 {
		t.Fatal("enabled overlay did not use library evidence")
	}
	if engine.cfg.LibraryEvidenceEnabled || engine.ranker.(*TransparentRanker).cfg.LibraryEvidenceEnabled || engine.cat != base {
		t.Fatal("overlay mutated shared engine configuration")
	}
	cat.reads = map[string]int{}
	enabled = false
	if _, err := engine.Build(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if len(cat.reads) != 0 {
		t.Fatalf("library evidence leaked into later disabled request: %v", cat.reads)
	}
	enabled = true
	intent.Controls.RecommendationMode = core.DeejAIOnly
	if _, err := engine.Build(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if len(cat.reads) != 0 {
		t.Fatal("shared evidence leaked into Deej-AI-only")
	}
	if !reflect.DeepEqual(modes, []core.RecommendationMode{core.EnhancedHybrid, core.EnhancedHybrid, core.DeejAIOnly}) || releases != 3 {
		t.Fatalf("modes=%v releases=%d", modes, releases)
	}
}

type failingDiscoveryRetriever struct {
	ports.CandidateRetriever
	err error
}

func (r failingDiscoveryRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	return nil, r.err
}

func TestDiscoveryOverlayReleasesOnErrorAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "retrieval error", true: "cancellation"}[canceled], func(t *testing.T) {
			base := testCatalog()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := errors.New("retrieval failed")
			profiles := &discoveryProfileFixture{Catalog: base}
			if canceled {
				want = context.Canceled
				profiles.onRead = func(ctx context.Context) error { cancel(); return ctx.Err() }
			}
			engine := New(base, fakes.NewSimilarityEngine(base), base, DefaultConfig())
			releases := 0
			engine.WithIntentOverlayProvider(func(_ context.Context, _ core.MusicIntent, _ ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (RequestOverlay, error) {
				return RequestOverlay{Catalog: profiles, Resolver: resolver, Retriever: failingDiscoveryRetriever{retriever, want}, Release: func() { releases++ }}, nil
			})
			intent := testIntent(2)
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			if _, err := engine.Build(ctx, intent); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if releases != 1 {
				t.Fatalf("release calls=%d", releases)
			}
		})
	}
}

func TestDiscoveryProfilesClearFreshStaleHintsAndPreserveRecordedHints(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh empty profile lookup", true: "recorded replay"}[recorded], func(t *testing.T) {
			base := testCatalog()
			cat := &discoveryProfileFixture{Catalog: base}
			source := &discoveryIntentRecorder{}
			engine := New(cat, fakes.NewSimilarityEngine(base), base, DefaultConfig()).WithCandidateSource(source)
			intent := testIntent(2)
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			stale := []core.DiscoveryProfile{{Artist: "Previous artist", Moods: []string{"calm"}, PackIDs: []string{"old-pack"}}}
			intent.Knowledge = &core.KnowledgeSnapshot{DiscoveryRecorded: recorded, PackProfiles: stale}
			playlist, err := engine.Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			if len(source.seen) != 1 {
				t.Fatalf("discovery opened %d times", len(source.seen))
			}
			if recorded {
				if cat.calls != 0 || !reflect.DeepEqual(source.seen[0].Knowledge.PackProfiles, stale) {
					t.Fatalf("recorded snapshot requeried or changed: calls=%d knowledge=%+v", cat.calls, source.seen[0].Knowledge)
				}
			} else {
				if cat.calls != 1 || len(source.seen[0].Knowledge.PackProfiles) != 0 || len(playlist.Intent.Knowledge.PackProfiles) != 0 {
					t.Fatalf("stale profiles reached discovery: calls=%d knowledge=%+v", cat.calls, source.seen[0].Knowledge)
				}
			}
			if !reflect.DeepEqual(intent.Knowledge.PackProfiles, stale) {
				t.Fatal("request mutated caller's history snapshot")
			}
		})
	}
}

func TestDiscoveryGenerationMismatchStopsRecommendation(t *testing.T) {
	base := testCatalog()
	source := &discoveryIntentRecorder{err: ports.ErrDiscoveryGenerationMismatch}
	engine := New(base, fakes.NewSimilarityEngine(base), base, DefaultConfig()).WithCandidateSource(source)
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Knowledge = &core.KnowledgeSnapshot{DiscoveryRecorded: true, PackProfiles: []core.DiscoveryProfile{{Artist: "Saved artist", PackIDs: []string{"retired-pack"}}}}
	playlist, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if !errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
		t.Fatalf("incompatible replay degraded into fresh catalog fallback: playlist=%+v err=%v", playlist, err)
	}
	if len(playlist.Tracks) != 0 {
		t.Fatal("incompatible replay produced replacement tracks")
	}
}

func TestDiscoveryGenerationMismatchPreflightBeforeRequiredOnlyPlaylist(t *testing.T) {
	base := testCatalog()
	source := &discoveryIntentRecorder{replayErr: ports.ErrDiscoveryGenerationMismatch}
	engine := New(base, fakes.NewSimilarityEngine(base), base, DefaultConfig()).WithCandidateSource(source)
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "audio"}, {Kind: core.ReferenceTrack, TrackID: "cooc"}}
	intent.Knowledge = &core.KnowledgeSnapshot{DiscoveryRecorded: true, PackProfiles: []core.DiscoveryProfile{{Artist: "Saved artist", PackIDs: []string{"retired-pack"}}}}
	playlist, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if !errors.Is(err, ports.ErrDiscoveryGenerationMismatch) || len(playlist.Tracks) != 0 {
		t.Fatalf("required-only request skipped compatibility check: tracks=%v err=%v", playlist.Tracks, err)
	}
	if source.nextCalls != 0 {
		t.Fatalf("preflight consumed stream %d times", source.nextCalls)
	}
}
