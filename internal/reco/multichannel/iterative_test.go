package multichannel

import (
	"context"
	"fmt"
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

func TestMetadataDiscoveryStopsWhenFinalPlaylistCanBeFilled(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		for _, required := range []int{0, 2} {
			for _, rejected := range []int{0, 3} {
				t.Run(fmt.Sprintf("%s/required=%d/rejected=%d", mode, required, rejected), func(t *testing.T) {
					cat, service, retriever := recommendationPoolFixture(t, 40, rejected)
					source := &fixtureDiscovery{}
					for _, candidate := range retriever.candidates {
						source.tracks = append(source.tracks, candidate.Track)
					}
					intent := poolIntent(10)
					intent.Controls.RecommendationMode = mode
					for i := 0; i < required; i++ {
						intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, TrackID: fmt.Sprintf("p%03d", 39-i)})
					}
					engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source).WithAudioProvider(func() *audio.Service { return service })
					engine.retriever = retriever
					checked := 0
					got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(core.TrackRef) { checked++ }})
					if err != nil || len(got.Tracks) != 10 || got.Outcome.State != core.OutcomeFulfilled {
						t.Fatalf("tracks=%d outcome=%+v err=%v", len(got.Tracks), got.Outcome, err)
					}
					if source.pulls != 10-required+rejected || checked != 10-required || len(retriever.calls) != 0 {
						t.Fatalf("unnecessary work: pulls=%d checked=%d retrievals=%d", source.pulls, checked, len(retriever.calls))
					}
					if got.AudioEvidence == nil || len(got.AudioEvidence.Assessments) != 10+rejected {
						t.Fatalf("unexpected audio work: %+v", got.AudioEvidence)
					}
				})
			}
		}
	}
}

func TestIterativeDiscoveryRejectsAndAdvancesUntilCount(t *testing.T) {
	cat := testCatalog()
	service, fetch := cachedAudioService(t, cat)
	source := &fixtureDiscovery{tracks: refs(cat, "blocked", "cooc", "audio", "last")}
	intent := testIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Blocked Artist"}}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source).WithAudioProvider(func() *audio.Service { return service })
	result, err := engine.Build(context.Background(), intent)
	// Stop at the first valid complete playlist: do not analyze "last" solely
	// to improve ranking after the requested count is already satisfied.
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "audio" || source.pulls != 3 {
		t.Fatalf("result=%+v pulls=%d err=%v", result, source.pulls, err)
	}
	if fetch.calls != 0 {
		t.Fatal("cached features were downloaded again")
	}
	if len(result.Intent.Knowledge.Discovery) != 3 {
		t.Fatal("rejected attempts were not saved")
	}
}

func TestBestAvailableDiscoveryDoesNotExtendForSoftArtistDiversity(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		t.Run(string(mode), func(t *testing.T) {
			var artists []string
			for i := range 20 {
				artists = append(artists, fmt.Sprintf("Artist %d", i%2))
			}
			cat, service, retriever := recommendationPoolFixture(t, 20, 0, artists...)
			source := &fixtureDiscovery{}
			for _, candidate := range retriever.candidates {
				source.tracks = append(source.tracks, candidate.Track)
			}
			intent := poolIntent(10)
			intent.VerificationPolicy = core.BestAvailable
			intent.References = nil
			intent.Seeds = core.IntentSeeds{}
			intent.Controls.RecommendationMode = mode
			intent.Controls.ArtistDiversity = 1
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source).WithAudioProvider(func() *audio.Service { return service })
			engine.retriever = retriever
			got, err := engine.Build(context.Background(), intent)
			// The complete cached pool now avoids provider discovery entirely;
			// a genre-only request still stops after N actual assessments.
			if err != nil || len(got.Tracks) != 10 || source.pulls != 0 || len(retriever.calls) != 0 {
				t.Fatalf("tracks=%d pulls=%d retrievals=%d err=%v", len(got.Tracks), source.pulls, len(retriever.calls), err)
			}
			if got.AudioEvidence == nil || len(got.AudioEvidence.Assessments) != 10 {
				t.Fatalf("surplus analysis: %+v", got.AudioEvidence)
			}
			for i := 1; i < len(got.Tracks); i++ {
				if got.Tracks[i].Artist == got.Tracks[i-1].Artist {
					t.Fatal("artist adjacency bypassed")
				}
			}
		})
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
