package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type mertRefillRetriever struct {
	cat   ports.Catalog
	pages [][]string
	calls int
}

func (r *mertRefillRetriever) Retrieve(_ context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	r.calls++
	for _, page := range r.pages {
		var out []core.Candidate
		for _, id := range page {
			if _, attempted := request.AttemptedIDs[id]; attempted {
				continue
			}
			meta, ok := r.cat.Meta(id)
			if ok {
				out = append(out, core.Candidate{Track: meta.Ref, Sources: []core.RetrievalEvidence{{Channel: ChannelSeedAudio, QueryID: "seed", Rank: 1, Score: 1, QueryWeight: 1}}})
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, nil
}

func TestMERTDiscoveryAndDeejAIRefillMeetRequestedCountAndReplay(t *testing.T) {
	cat, input := enhancedFixture(t)
	meta, _ := cat.Meta("c")
	hit := core.AudioRepresentation{
		ID: "mert-c", TrackID: "c", TrackKey: core.ProvisionalRecordingKey(meta.Ref),
		CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, 0},
	}
	input.Representations["c"] = hit
	retriever := &mertRefillRetriever{cat: cat, pages: [][]string{{"a"}, {"b"}}}
	searchCalls := 0
	engine := New(cat, nil, cat, DefaultConfig())
	engine.retriever = retriever
	engine.WithMERTSimilaritySearchProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, queries []core.MERTSimilarityQuery, _ map[string]struct{}, _ int) (*core.MERTSimilaritySearch, error) {
		searchCalls++
		if len(queries) != 1 || queries[0].Track.ID != "seed" {
			t.Fatalf("unexpected MERT queries: %+v", queries)
		}
		queries[0].RepresentationID = "mert-seed"
		return &core.MERTSimilaritySearch{
			Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
			ViewFingerprint: "view-1", SearchableTracks: 3, Queries: queries,
			Hits: []core.MERTSimilarityHit{{
				GroupID: queries[0].GroupID, QueryTrackID: "seed", TrackID: "c",
				Rank: 1, Score: .99, QueryWeight: 1, Representation: hit,
			}},
		}, nil
	})
	engine.WithEnhancedAudioProvider(func(context.Context, core.MusicIntent, core.TasteProfile, []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		return core.NewEnhancedAudioSnapshot(input)
	})
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 3 || searchCalls != 1 || retriever.calls < 2 {
		t.Fatalf("tracks=%v search=%d Deej-AI pages=%d", playlist.Tracks, searchCalls, retriever.calls)
	}
	foundMERT := false
	for _, reason := range playlist.Rationale {
		for _, source := range reason.Sources {
			foundMERT = foundMERT || reason.TrackID == "c" && source.Channel == ChannelMERTAudio
		}
	}
	if !foundMERT || playlist.EnhancedAudio == nil || playlist.EnhancedAudio.Input().MERTSearch == nil {
		t.Fatal("MERT-only neighbor or frozen search evidence missing")
	}

	replayed, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: playlist.EnhancedAudio})
	if err != nil || searchCalls != 1 || len(replayed.Tracks) != len(playlist.Tracks) {
		t.Fatalf("replay searched or changed result: calls=%d tracks=%v err=%v", searchCalls, replayed.Tracks, err)
	}
}

func TestEnhancedHybridUsesDeejAIRefillWithoutMERTCoverage(t *testing.T) {
	cat, _ := enhancedFixture(t)
	retriever := &mertRefillRetriever{cat: cat, pages: [][]string{{"a"}, {"b"}, {"c"}}}
	engine := New(cat, nil, cat, DefaultConfig())
	engine.retriever = retriever
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 3 || retriever.calls < 3 {
		t.Fatalf("tracks=%v Deej-AI pages=%d", playlist.Tracks, retriever.calls)
	}
}

func TestMERTNeighborStillObeysHardArtistExclusion(t *testing.T) {
	cat, input := enhancedFixture(t)
	meta, _ := cat.Meta("c")
	hit := core.AudioRepresentation{ID: "mert-c", TrackID: "c", TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, 0}}
	search := &core.MERTSimilaritySearch{
		Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
		Queries: []core.MERTSimilarityQuery{{GroupID: "seed", Track: core.TrackRef{ID: "seed"}, Weight: 1, RepresentationID: "mert-seed"}},
		Hits:    []core.MERTSimilarityHit{{GroupID: "seed", QueryTrackID: "seed", TrackID: "c", Rank: 1, Score: 1, QueryWeight: 1, Representation: hit}},
	}
	engine := New(cat, nil, cat, DefaultConfig())
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Constraints.ArtistsExclude = []string{"Third"}
	result := engine.mertCandidates(search)
	eligible := newEligibility(intent, refs(cat, "seed"), nil)
	result, err := eligible.filter(context.Background(), result, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("excluded MERT neighbor survived: %+v", result)
	}

	search.Hits[0].Representation.Pooled = nil
	if result := engine.mertCandidates(search); len(result) != 0 {
		t.Fatalf("invalid frozen MERT evidence produced a candidate: %+v", result)
	}
}
