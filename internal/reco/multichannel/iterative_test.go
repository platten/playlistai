package multichannel

import (
	"context"
	"io"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type fixtureDiscovery struct {
	tracks   []core.TrackRef
	pulls    int
	snapshot core.KnowledgeSnapshot
}

func (s *fixtureDiscovery) OpenCandidates(core.MusicIntent, ports.Catalog, ports.ReferenceResolver) ports.MusicCandidateStream {
	if s.tracks == nil {
		return nil
	}
	return s
}
func (s *fixtureDiscovery) Next(ctx context.Context) (core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return core.TrackRef{}, err
	}
	if s.pulls >= len(s.tracks) {
		return core.TrackRef{}, io.EOF
	}
	track := s.tracks[s.pulls]
	s.pulls++
	s.snapshot.Discovery = append(s.snapshot.Discovery, track)
	return track, nil
}
func (s *fixtureDiscovery) Snapshot() *core.KnowledgeSnapshot { return &s.snapshot }

func TestIterativeDiscoveryRejectsAndAdvancesUntilCount(t *testing.T) {
	cat := testCatalog()
	service, fetch := cachedAudioService(t, cat)
	source := &fixtureDiscovery{tracks: refs(cat, "blocked", "cooc", "audio", "last")}
	intent := testIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Blocked Artist"}}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source).WithAudioProvider(func() *audio.Service { return service })
	result, err := engine.Build(context.Background(), intent)
	// Ranking now compares all eligible alternatives instead of returning the
	// first passing preview. "last" has higher combined seed/semantic affinity.
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "last" || source.pulls != 4 {
		t.Fatalf("result=%+v pulls=%d err=%v", result, source.pulls, err)
	}
	if fetch.calls != 0 {
		t.Fatal("cached features were downloaded again")
	}
	if len(result.Intent.Knowledge.Discovery) != 4 {
		t.Fatal("rejected attempts were not saved")
	}
}

func TestIterativeMissingPreviewDoesNotEndDiscovery(t *testing.T) {
	cat := testCatalog()
	service, fetch := cachedAudioService(t, cat, "audio")
	source := &fixtureDiscovery{tracks: refs(cat, "cooc", "audio")}
	intent := testIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	result, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source).WithAudioProvider(func() *audio.Service { return service }).Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "audio" || fetch.calls < 1 || fetch.calls > cat.Len() {
		t.Fatalf("missing-preview refill=%+v calls=%d err=%v", result.Tracks, fetch.calls, err)
	}
}

type advancingRetriever struct {
	cat   ports.Catalog
	calls []ports.RetrievalRequest
}

func (r *advancingRetriever) Retrieve(_ context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	r.calls = append(r.calls, request)
	for _, id := range []string{"audio", "last"} {
		if _, seen := request.AttemptedIDs[id]; !seen {
			meta, _ := r.cat.Meta(id)
			return []core.Candidate{{Track: meta.Ref}}, nil
		}
	}
	return nil, nil
}

func TestIterativeReferenceOnlySkipsAudioAndRetainsAnchor(t *testing.T) {
	cat := testCatalog()
	retriever := &advancingRetriever{cat: cat}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	engine.retriever = retriever
	intent := testIntent(2)
	intent.OriginalDescription = "Like Seed Artist, 2 tracks"
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 2 || result.AudioEvidence != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	// Both candidates are now prepared before analysis/selection. Preparation
	// must not misrepresent unaccepted candidates as listening/continuation data.
	if len(retriever.calls) != 3 {
		t.Fatal("shortlist was not prepared through catalog exhaustion")
	}
	for _, request := range retriever.calls {
		if request.Intent.References[0].TrackID != "seed" || len(request.RecentSelections) != 0 {
			t.Fatal("preparation changed original anchor or added unaccepted context")
		}
	}
}

func TestIterativeDiscoveryExhaustionAndCancellation(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{tracks: refs(cat, "cooc")}).WithAudioProvider(func() *audio.Service { return service })
	intent := testIntent(20)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) >= 20 || result.Outcome.State == core.OutcomeFulfilled {
		t.Fatalf("exhaustion lost: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Build(ctx, intent); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
}
