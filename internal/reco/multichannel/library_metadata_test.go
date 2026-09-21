package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type metadataFixture struct {
	ports.Catalog
	metadata  map[string]core.EnrichedTrack
	scores    map[string]float64
	conflicts map[string]bool
}

func (f metadataFixture) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	v, ok := f.metadata[id]
	return v, ok, ctx.Err()
}
func (f metadataFixture) LibraryTrackFeatures(context.Context, string) (core.LibraryTrackFeatures, bool) {
	return core.LibraryTrackFeatures{Conflicts: f.conflicts}, f.conflicts != nil
}
func (f metadataFixture) LibraryPreferenceScore(_ context.Context, id string, _ core.MusicIntent, _ string) (float64, bool) {
	v, ok := f.scores[id]
	return v, ok
}

func TestPackedMetadataRankingIsSourceNeutralAndModeBounded(t *testing.T) {
	cat := metadataFixture{Catalog: testCatalog(), scores: map[string]float64{"local:x": .8, "outside:y": .8, "negative": -.8}}
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	intent := testIntent(4)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	candidates := []core.Candidate{{Track: core.TrackRef{ID: "local:x"}}, {Track: core.TrackRef{ID: "outside:y"}}, {Track: core.TrackRef{ID: "unknown"}}, {Track: core.TrackRef{ID: "negative"}}}
	for i := range candidates {
		candidates[i].Scores.Total = .5
	}
	ranker := NewRanker(cat, cfg)
	if err := ranker.libraryMetadataScores(context.Background(), candidates, ports.RankRequest{Intent: intent}); err != nil {
		t.Fatal(err)
	}
	if candidates[0].Scores.Total != candidates[1].Scores.Total || candidates[2].Available.LibraryMetadata || candidates[2].Scores.Total <= candidates[3].Scores.Total {
		t.Fatalf("source or missingness bias: %+v", candidates)
	}
	before := candidates[0].Scores.Total
	intent.Controls.RecommendationMode = core.DeejAIOnly
	if err := ranker.libraryMetadataScores(context.Background(), candidates, ports.RankRequest{Intent: intent}); err != nil || candidates[0].Scores.Total != before {
		t.Fatal("mode policy changed")
	}
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ranker.libraryMetadataScores(ctx, candidates, ports.RankRequest{Intent: intent}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPackedMetadataRankingPreservesProviderComparisonWhenPackIsMissingOrNeutral(t *testing.T) {
	cat := metadataFixture{Catalog: testCatalog(), scores: map[string]float64{"audio": 0}}
	candidate := []core.Candidate{{Track: core.TrackRef{ID: "audio"}}}
	candidate[0].Scores.Total = .1
	candidate[0].Scores.LibraryMetadata, candidate[0].Available.LibraryMetadata = .5, true
	cfg := DefaultConfig()
	cfg.LibraryEvidenceEnabled = true
	intent := enhancedIntent(1)
	if err := NewRanker(cat, cfg).libraryMetadataScores(context.Background(), candidate, ports.RankRequest{Intent: intent}); err != nil {
		t.Fatal(err)
	}
	if !candidate[0].Available.LibraryMetadata || candidate[0].Scores.LibraryMetadata != .5 || candidate[0].Scores.Total <= .1 {
		t.Fatalf("provider comparison was erased: %+v", candidate[0])
	}
}

func TestPackedMetadataDatesPreserveOriginalVersusEditionAndConflicts(t *testing.T) {
	cat := metadataFixture{Catalog: testCatalog(), metadata: map[string]core.EnrichedTrack{"audio": {OriginalReleaseDate: "1994", ReleaseEditionDate: "2024", Year: 2024, IdentityStatus: core.ResolutionResolved}}}
	o := New(cat, nil, testCatalog(), DefaultConfig())
	o.requestContext = context.Background()
	m, ok := o.knowledgeTrack("audio")
	if !ok || m.OriginalReleaseDate != "1994" || m.ReleaseEditionDate != "2024" {
		t.Fatalf("dates unavailable: %+v", m)
	}
	conflict := mergeRecordingMetadata(core.EnrichedTrack{OriginalReleaseDate: "2001"}, m)
	if conflict.OriginalReleaseDate != "" {
		t.Fatal("contradiction silently preferred")
	}
	noOriginal := mergeRecordingMetadata(core.EnrichedTrack{}, core.EnrichedTrack{Year: 1994, ReleaseEditionDate: "1994"})
	if noOriginal.OriginalReleaseDate != "" {
		t.Fatal("edition promoted to original")
	}
	cat.conflicts = map[string]bool{"original_release_date": true}
	cat.metadata["audio"] = core.EnrichedTrack{IdentityStatus: core.ResolutionResolved}
	o.cat = cat
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: core.TrackRef{ID: "audio"}, OriginalReleaseDate: "1994"}}}
	m, _ = o.knowledgeTrack("audio")
	if m.OriginalReleaseDate != "" {
		t.Fatal("archive resurrected conflicting pack date")
	}
}
