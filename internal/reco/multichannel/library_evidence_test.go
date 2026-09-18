package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type libraryEvidenceFixture struct {
	*fakes.Catalog
	vectors map[string]core.LibraryVector
	dsp     map[string]float64
}

func (c libraryEvidenceFixture) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	v, ok := c.vectors[id]
	return v, ok, ctx.Err()
}
func (c libraryEvidenceFixture) LibraryDSPPreference(_ context.Context, id string, _ core.MusicIntent) (float64, bool) {
	v, ok := c.dsp[id]
	return v, ok
}

func TestLibraryRankingSignedDSPAndMissingEvidence(t *testing.T) {
	cat := libraryEvidenceFixture{Catalog: testCatalog(), dsp: map[string]float64{"audio": 1, "far": -1}}
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	ranker := NewRanker(cat, cfg)
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	candidates := []core.Candidate{{Track: core.TrackRef{ID: "audio"}}, {Track: core.TrackRef{ID: "other"}}, {Track: core.TrackRef{ID: "far"}}}
	for i := range candidates {
		candidates[i].Scores.Total = .5
	}
	if err := ranker.libraryScores(context.Background(), candidates, ports.RankRequest{Intent: intent}); err != nil {
		t.Fatal(err)
	}
	if !(candidates[0].Scores.Total > candidates[1].Scores.Total && candidates[1].Scores.Total > candidates[2].Scores.Total) {
		t.Fatalf("signed ordering lost: %+v", candidates)
	}
	if candidates[1].Available.LibraryDSP {
		t.Fatal("missing DSP treated as observed")
	}
}

func TestLibraryRankingContractsModesAndCancellation(t *testing.T) {
	vector := func(space string, values ...float32) core.LibraryVector {
		return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: space}, Values: values}
	}
	cat := libraryEvidenceFixture{Catalog: testCatalog(), vectors: map[string]core.LibraryVector{"seed": vector("mert", 1, 0), "audio": vector("other-model", 1, 0), "far": vector("mert", -1, 0)}}
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	candidates := []core.Candidate{{Track: core.TrackRef{ID: "audio"}}, {Track: core.TrackRef{ID: "far"}}}
	ranker := NewRanker(cat, cfg)
	if err := ranker.libraryScores(context.Background(), candidates, ports.RankRequest{Intent: intent}); err != nil {
		t.Fatal(err)
	}
	if candidates[0].Available.LibraryMERT || !candidates[1].Available.LibraryMERT || candidates[1].Scores.LibraryMERT != -1 {
		t.Fatalf("incompatible spaces mixed: %+v", candidates)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ranker.libraryScores(ctx, candidates, ports.RankRequest{Intent: intent}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, enabled := range []bool{false, true} {
		cfg.LibraryEvidenceEnabled = enabled
		if enabled {
			intent.Controls.RecommendationMode = core.DeejAIOnly
		}
		candidate := []core.Candidate{{Track: core.TrackRef{ID: "far"}}}
		candidate[0].Scores.Total = .7
		if err := NewRanker(cat, cfg).libraryScores(context.Background(), candidate, ports.RankRequest{Intent: intent}); err != nil {
			t.Fatal(err)
		}
		if candidate[0].Scores.Total != .7 || candidate[0].Available.LibraryMERT {
			t.Fatal("experimental evidence leaked into disabled mode")
		}
	}
}

func TestEnhancedReferenceFloorRequiresGroundedPositiveLocalComparison(t *testing.T) {
	intent := testIntent(2)
	candidate := core.Candidate{Sources: []core.RetrievalEvidence{{Channel: "library_mert", QueryID: "seed", Score: .8, LibrarySource: &core.LibraryEvidenceSource{SpaceID: "compatible-space"}}}}
	if score, ok := enhancedRequestRelevance(candidate, intent); !ok || score != .8 {
		t.Fatalf("lost direct local reference comparison: %v %v", score, ok)
	}
	candidate.Sources[0].QueryID = "unrelated"
	if _, ok := enhancedRequestRelevance(candidate, intent); ok {
		t.Fatal("unrelated retrieval became request evidence")
	}
	candidate.Sources[0].QueryID = "seed"
	intent.References[0].Influence = core.InfluenceNegative
	if _, ok := enhancedRequestRelevance(candidate, intent); ok {
		t.Fatal("negative anchor became positive support")
	}
	intent.References[0].Influence = core.InfluencePositive
	candidate.Sources[0].LibrarySource = nil
	if _, ok := enhancedRequestRelevance(candidate, intent); ok {
		t.Fatal("unprovenanced similarity accepted")
	}
	candidate.Sources[0].LibrarySource = &core.LibraryEvidenceSource{SpaceID: "compatible-space"}
	intent.RequiredTracks = intent.References
	intent.References = nil
	if _, ok := enhancedRequestRelevance(candidate, intent); !ok {
		t.Fatal("required-only anchor lost its direct neighbors")
	}
	intent.References = []core.IntentReference{{TrackID: "different", Influence: core.InfluencePositive}}
	if _, ok := enhancedRequestRelevance(candidate, intent); ok {
		t.Fatal("required anchor displaced explicit reference priority")
	}
}
