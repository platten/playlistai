package multichannel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestEnhancedComparesLaterPagesBeyondFilledCount(t *testing.T) {
	rows := []fakes.CatalogTrack{{ID: "seed", Display: "Reference - Seed", Audio: []float32{1, 0}, Track: []float32{1, 0}}}
	var ids []string
	for i := range 48 {
		id := fmt.Sprintf("candidate-%03d", i)
		vector := []float32{.5, .5}
		if i == 47 {
			vector = []float32{1, 0}
		}
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Same Artist - " + id, Audio: vector, Track: vector})
		ids = append(ids, id)
	}
	cat := fakes.NewCatalog(2, rows...)
	retriever := &poolRetriever{candidates: candidatesForTracks(refs(cat, ids...)), pageSize: 8}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	engine.retriever = retriever
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Controls.ArtistDiversity = 0
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "candidate-047" {
		t.Fatalf("late stronger candidate lost: %+v %v", result.Tracks, err)
	}
	if result.Search == nil {
		t.Fatal("missing frozen search")
	}
	if result.Search.Considered != 48 || result.Search.Eligible != 48 {
		t.Fatalf("did not compare full source: considered=%d eligible=%d reason=%s", result.Search.Considered, result.Search.Eligible, result.Search.StopReason)
	}
}

func TestEnhancedAssessmentCeilingIncludesSeedPlanning(t *testing.T) {
	rows := []fakes.CatalogTrack{{ID: "seed", Display: "Reference - Seed", Audio: []float32{1, 0}, Track: []float32{1, 0}}}
	var ids []string
	for i := range 600 {
		id := fmt.Sprintf("candidate-%03d", i)
		ids = append(ids, id)
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Artist - " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
	}
	cat := fakes.NewCatalog(2, rows...)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	engine.retriever = &poolRetriever{candidates: candidatesForTracks(refs(cat, ids...)), pageSize: 32}
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	result, err := engine.Build(context.Background(), intent)
	if err != nil || result.Search == nil {
		t.Fatalf("missing search: err=%v", err)
	}
	if result.Search.Considered != 512 || len(result.Search.Candidates) != 512 || result.Search.StopReason != "candidate_limit" {
		t.Fatalf("assessment ceiling lost: considered=%d snapshot=%d reason=%s", result.Search.Considered, len(result.Search.Candidates), result.Search.StopReason)
	}
	// The same admission ledger is reused by seed planning and comparison.
	engine.enhanced = true
	engine.search = &core.SearchSnapshot{}
	candidate := core.Candidate{Track: core.TrackRef{ID: "reused"}}
	firstAdmission := engine.admitCandidateAssessment(candidate)
	secondAdmission := engine.admitCandidateAssessment(candidate)
	if !firstAdmission || !secondAdmission || engine.search.Considered != 1 {
		t.Fatal("repeated seed/candidate spent another assessment admission")
	}
}

func TestEnhancedRoundsGiveEachSourceAndStageOpportunity(t *testing.T) {
	var candidates []core.Candidate
	for _, source := range []string{"packed:start", "packed:end", "online:start"} {
		for i := range 70 {
			candidates = append(candidates, core.Candidate{Track: core.TrackRef{ID: fmt.Sprintf("%s:%d", source, i)}, Sources: []core.RetrievalEvidence{{Channel: source, QueryID: source}}})
		}
	}
	batch, remaining := nextCandidateRound(roundRobinCandidateSources(candidates))
	counts := map[string]int{}
	for _, candidate := range batch {
		counts[candidate.Sources[0].Channel]++
	}
	if len(batch) != 32 || len(remaining) != 178 || len(counts) != 3 {
		t.Fatalf("round lost source coverage: %v remaining=%d", counts, len(remaining))
	}
	for source, count := range counts {
		if count < 10 || count > 11 {
			t.Fatalf("%s did not receive an interleaved opportunity: %d", source, count)
		}
	}
}

func TestRetrievalRetainsPartialAndTriesLaterSource(t *testing.T) {
	byID := map[string]*core.Candidate{}
	var exploration []explorationOption
	jobs := []retrievalJob{{run: func(out map[string]*core.Candidate, _ *[]explorationOption) error {
		out["partial"] = &core.Candidate{Track: core.TrackRef{ID: "partial"}}
		return context.DeadlineExceeded
	}}, {run: func(out map[string]*core.Candidate, _ *[]explorationOption) error {
		out["later"] = &core.Candidate{Track: core.TrackRef{ID: "later"}}
		return nil
	}}}
	err := runRetrievalJobs(context.Background(), jobs, byID, &exploration)
	if !errors.Is(err, context.DeadlineExceeded) || len(byID) != 2 {
		t.Fatalf("partial source hid later results: %v %v", byID, err)
	}
}

type fairArtistCatalog struct {
	metadataPriorityCatalog
	ids []string
}

func (c fairArtistCatalog) ArtistRecordings(context.Context, string) ([]core.TrackRef, error) {
	return refs(c, c.ids...), nil
}

func TestEnhancedSeedPlanningLeavesLaterProviderOpportunity(t *testing.T) {
	for _, explicitArtist := range []bool{false, true} {
		t.Run(fmt.Sprintf("artist-reference=%t", explicitArtist), func(t *testing.T) {
			var rows []fakes.CatalogTrack
			var ids []string
			semantic := &semanticFixture{}
			for i := range 600 {
				id := fmt.Sprintf("local-%03d", i)
				ids = append(ids, id)
				rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Catalog Artist - " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
				semantic.positive = append(semantic.positive, core.SemanticHit{TrackID: id, Score: .2})
			}
			rows = append(rows, fakes.CatalogTrack{ID: "provider-best", Display: "Provider Artist - Best", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			semantic.positive = append(semantic.positive, core.SemanticHit{TrackID: "provider-best", Score: .9})
			base := fakes.NewCatalog(2, rows...)
			cat := fairArtistCatalog{metadataPriorityCatalog{base}, ids}
			_, intent := localPriorityFixture()
			if explicitArtist {
				intent.Knowledge = &core.KnowledgeSnapshot{PackProfiles: []core.DiscoveryProfile{{Artist: "Catalog Artist", PackIDs: []string{"fixture"}}}}
				intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Catalog Artist", TrackID: ids[0], Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: cat.CatalogVersion(), Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: "catalog-artist", Artist: "Catalog Artist", Representatives: []core.WeightedTrack{{TrackID: ids[0], Weight: 1}}}}}}
			}
			source := &fixtureDiscovery{tracks: refs(cat, "provider-best")}
			engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(base), cat, semantic, semantic, DefaultConfig()).WithCandidateSource(source)
			engine.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, ids...))}}
			result, err := engine.Build(context.Background(), intent)
			if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "provider-best" {
				t.Fatalf("planning crowded out better provider candidate: tracks=%v err=%v pulls=%d", result.Tracks, err, source.pulls)
			}
			if result.Search == nil {
				t.Fatal("missing frozen search")
			}
			if result.Search.Considered < enhancedMinimumComparisons || result.Search.Considered > enhancedCandidateLimit || source.pulls != 1 || result.Search.StopReason != "quality_target" {
				t.Fatalf("unbounded or unfair comparison: considered=%d pulls=%d reason=%s", result.Search.Considered, source.pulls, result.Search.StopReason)
			}
		})
	}
}

// Unlike metadataPriorityRetriever, this preserves independent source buckets.
type bucketedMetadataRetriever struct{ poolRetriever }

func (*bucketedMetadataRetriever) SupportsIntentMetadata() bool { return true }

func TestEnhancedSixteenSourceUnionLeavesProviderOpportunity(t *testing.T) {
	var rows []fakes.CatalogTrack
	var candidates []core.Candidate
	semantic := &semanticFixture{}
	for source := range 16 {
		channel := fmt.Sprintf("local-source-%02d", source)
		for rank := range 32 {
			id := fmt.Sprintf("local-%02d-%02d", source, rank)
			track := core.TrackRef{ID: id, Artist: "Catalog Artist", Title: id}
			rows = append(rows, fakes.CatalogTrack{ID: id, Display: track.Display(), Audio: []float32{1, 0}, Track: []float32{1, 0}})
			candidates = append(candidates, core.Candidate{Track: track, Sources: []core.RetrievalEvidence{{Channel: channel, QueryID: channel, Rank: rank + 1, QueryWeight: 1}}})
			semantic.positive = append(semantic.positive, core.SemanticHit{TrackID: id, Score: .2})
		}
	}
	rows = append(rows, fakes.CatalogTrack{ID: "provider-best", Display: "Provider Artist - Best", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	semantic.positive = append(semantic.positive, core.SemanticHit{TrackID: "provider-best", Score: .9})
	base := fakes.NewCatalog(2, rows...)
	cat := metadataPriorityCatalog{base}
	_, intent := localPriorityFixture()
	source := &fixtureDiscovery{tracks: refs(cat, "provider-best")}
	engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(base), cat, semantic, semantic, DefaultConfig()).WithCandidateSource(source)
	engine.retriever = &bucketedMetadataRetriever{poolRetriever{candidates: candidates}}
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].ID != "provider-best" {
		t.Fatalf("sixteen local sources hid stronger provider candidate: ids=%v pulls=%d err=%v", result.IDs(), source.pulls, err)
	}
	if result.Search == nil {
		t.Fatal("missing frozen search")
	}
	if result.Search.Considered < enhancedMinimumComparisons || result.Search.Considered > enhancedCandidateLimit || len(result.Search.Candidates) != result.Search.Considered || result.Search.StopReason != "quality_target" {
		t.Fatalf("quality target bypassed bounded comparison: considered=%d snapshot=%d reason=%s", result.Search.Considered, len(result.Search.Candidates), result.Search.StopReason)
	}
	providerAt := -1
	localSources := map[string]bool{}
	for index, candidate := range result.Search.Candidates {
		if candidate.Track.ID == "provider-best" {
			providerAt = index
		}
		for _, evidence := range candidate.Sources {
			if evidence.Channel != "metadata_discovery" {
				localSources[evidence.Channel] = true
			}
		}
	}
	// Planning may validate 32 distinct seeds before the first 32-track page.
	if providerAt < 0 || providerAt > 2*enhancedChannelBatch || len(localSources) != 16 || source.pulls != 1 {
		t.Fatalf("source opportunity lost: provider-index=%d local-sources=%d pulls=%d", providerAt, len(localSources), source.pulls)
	}
}

type partialErrorMetadataRetriever struct {
	poolRetriever
	repeat bool
}

func (*partialErrorMetadataRetriever) SupportsIntentMetadata() bool { return true }
func (r *partialErrorMetadataRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	if r.repeat {
		request.AttemptedIDs = nil
	}
	candidates, err := r.poolRetriever.Retrieve(ctx, request)
	if err != nil {
		return candidates, err
	}
	// A source timed out while the other sources completed this page.
	return candidates, context.DeadlineExceeded
}

func TestEnhancedPartialSourceErrorKeepsHealthyLaterPages(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprintf("nonadvancing=%t", repeat), func(t *testing.T) {
			var rows []fakes.CatalogTrack
			var ids []string
			semantic := &semanticFixture{}
			for index := range 48 {
				id := fmt.Sprintf("local-%02d", index)
				ids = append(ids, id)
				rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Catalog Artist - " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
				score := .2
				if index == 47 {
					score = .9
				}
				semantic.positive = append(semantic.positive, core.SemanticHit{TrackID: id, Score: score})
			}
			base := fakes.NewCatalog(2, rows...)
			cat := metadataPriorityCatalog{base}
			_, intent := localPriorityFixture()
			retriever := &partialErrorMetadataRetriever{poolRetriever: poolRetriever{candidates: candidatesForTracks(refs(cat, ids...)), pageSize: 8}, repeat: repeat}
			engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(base), cat, semantic, semantic, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
			engine.retriever = retriever
			result, err := engine.Build(context.Background(), intent)
			if err != nil || len(result.Tracks) != 1 || result.Search == nil {
				t.Fatalf("partial source lost completed candidates: ids=%v err=%v", result.IDs(), err)
			}
			if repeat {
				if len(retriever.calls) > 2 || result.Search.Considered != 8 {
					t.Fatalf("nonadvancing partial source retried: calls=%d considered=%d", len(retriever.calls), result.Search.Considered)
				}
			} else if result.Tracks[0].ID != "local-47" || result.Search.Considered != 48 {
				t.Fatalf("source error hid stronger later candidate: ids=%v considered=%d calls=%d", result.IDs(), result.Search.Considered, len(retriever.calls))
			}
			if result.Search.StopReason != "source_interrupted" {
				t.Fatalf("timeout reported as exhaustion: %s", result.Search.StopReason)
			}
		})
	}
}
