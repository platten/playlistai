package localcatalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func TestDuplicateFilesDoNotBoostFusionOrPenalizeOtherRecordings(t *testing.T) {
	run := func(duplicates bool) map[string]float64 {
		tracks := []librarypack.Track{
			{ID: "a", Artist: "Artist", Title: "First", RecordingIdentity: "musicbrainz:11111111-1111-4111-8111-111111111111"},
			{ID: "z", Artist: "Artist", Title: "Last", RecordingIdentity: "musicbrainz:22222222-2222-4222-8222-222222222222"},
		}
		if duplicates {
			copy := tracks[0]
			copy.ID = "b"
			tracks = append(tracks, copy)
		}
		local, manager := openTestCatalog(t, tracks, nil)
		defer manager.Close()
		defer local.Close()
		retriever, err := NewCombinedRetriever(baseRetriever{}, local, ModeLibraryOnly, 2)
		if err != nil {
			t.Fatal(err)
		}
		candidates, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: core.MusicIntent{
			References: []core.IntentReference{{Query: "Artist"}, {Query: "ARTIST"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 2 {
			t.Fatalf("got %d candidates: %+v", len(candidates), candidates)
		}
		scores := map[string]float64{}
		for _, candidate := range candidates {
			if len(candidate.Sources) != 1 {
				t.Fatalf("copies/repeated query multiplied evidence: %+v", candidate)
			}
			scores[candidate.Track.Title] = candidate.Scores.RetrievalFusion
		}
		return scores
	}
	before, after := run(false), run(true)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("duplicate file changed normalized RRF: before=%v after=%v", before, after)
	}
}

func TestUniqueEvidenceRetainsIndependentQueriesProvenanceAndSpaces(t *testing.T) {
	space := librarypack.VectorSpace{Name: "mert", ModelRevision: "v1"}
	otherSpace := space
	otherSpace.ModelRevision = "v2"
	first := Evidence{Channel: MERTChannel, QueryID: "seed", Rank: 4, Score: .7, Provenance: Provenance{PackID: "pack", MERTGeneration: "one"}, VectorSpace: &space}
	better := first
	better.Rank, better.Score = 2, .8
	otherQuery := first
	otherQuery.QueryID = "other-seed"
	otherGeneration := first
	otherGeneration.Provenance.MERTGeneration = "two"
	otherModel := first
	otherModel.VectorSpace = &otherSpace
	input := []Evidence{first, otherQuery, otherGeneration, otherModel, better, better}
	result := uniqueEvidence(input)
	if len(result) != 4 {
		t.Fatalf("independent observations lost or copies retained: %+v", result)
	}
	for i, j := 0, len(input)-1; i < j; i, j = i+1, j-1 {
		input[i], input[j] = input[j], input[i]
	}
	if !reflect.DeepEqual(result, uniqueEvidence(input)) {
		t.Fatal("evidence merge depends on input order")
	}
	if result[0].Rank != 2 || result[0].Score != .8 {
		t.Fatalf("best observation lost: %+v", result[0])
	}
}

type evidenceBaseRetriever struct{ candidates []core.Candidate }

func (r evidenceBaseRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	return r.candidates, nil
}

func TestCombinedRetrieverMergesBasePackAliasEvidenceOnce(t *testing.T) {
	identity := "musicbrainz:11111111-1111-4111-8111-111111111111"
	local, manager := openTestCatalog(t, []librarypack.Track{{ID: "a", Artist: "Artist", Title: "Song", RecordingIdentity: identity}}, nil)
	defer manager.Close()
	defer local.Close()
	source := local.EvidenceSource()
	source.SpaceID, source.Generation, source.Scope = "", local.provenance.MetadataGeneration, "embedded_tags"
	observation := core.RetrievalEvidence{Channel: MetadataChannel, QueryID: "artist", Rank: 1, Score: 100, QueryWeight: 1, LibrarySource: &source}
	base := evidenceBaseRetriever{candidates: []core.Candidate{{Track: core.TrackRef{ID: "base", Artist: "Artist", Title: "Song", RecordingIdentity: identity}, Sources: []core.RetrievalEvidence{observation, observation}}}}
	retriever, err := NewCombinedRetriever(base, local, ModeCombined, 2)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{{Query: "Artist"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Track.ID != "base" || len(candidates[0].Sources) != 1 || !reflect.DeepEqual(candidates[0].Sources[0], observation) {
		t.Fatalf("alias contribution/provenance incorrect: %+v", candidates)
	}
}

func TestRetrievalEvidenceDoesNotCombineIndependentSpaces(t *testing.T) {
	a := core.RetrievalEvidence{Channel: MERTChannel, QueryID: "seed", Rank: 1, QueryWeight: 1, LibrarySource: &core.LibraryEvidenceSource{PackID: "pack", SpaceID: "v1", Generation: "one"}}
	b := a
	b.LibrarySource = &core.LibraryEvidenceSource{PackID: "pack", SpaceID: "v2", Generation: "one"}
	c := a
	c.LibrarySource = &core.LibraryEvidenceSource{PackID: "pack", SpaceID: "v1", Generation: "two"}
	if got := uniqueRetrievalEvidence([]core.RetrievalEvidence{a, a, b, c}); len(got) != 3 {
		t.Fatalf("independent spaces/generations collapsed: %+v", got)
	}
}

func TestQueryIdentityPreservesTypedCriteriaAndExternalVectors(t *testing.T) {
	metadata := independentQueryID(MetadataQuery{Text: "Ambient"})
	genre := independentQueryID(MetadataQuery{Text: "Ambient", Criterion: &core.MusicalCriterion{Kind: "genre", Value: "ambient"}})
	mood := independentQueryID(MetadataQuery{Text: "Ambient", Criterion: &core.MusicalCriterion{Kind: "mood", Value: "ambient"}})
	if metadata == genre || genre == mood || metadata == mood {
		t.Fatal("typed queries were combined")
	}
	a := independentQueryID(NeighborQuery{Vector: []float32{1, 0}})
	b := independentQueryID(NeighborQuery{Vector: []float32{0, 1}})
	if a == "" || a == b || a != independentQueryID(NeighborQuery{Vector: []float32{1, 0}}) {
		t.Fatal("external vector query identity is missing or unstable")
	}
}

func TestDuplicateSeedFilesUseOneRecordingQuery(t *testing.T) {
	identity := "musicbrainz:11111111-1111-4111-8111-111111111111"
	local, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "seed-a", Artist: "Artist", Title: "Seed", RecordingIdentity: identity},
		{ID: "seed-b", Artist: "Artist", Title: "Seed", RecordingIdentity: identity},
		{ID: "result", Artist: "Artist", Title: "Result"},
	}, nil)
	defer manager.Close()
	defer local.Close()
	executor, err := NewExecutor(local, 2)
	if err != nil {
		t.Fatal(err)
	}
	executor.mert = func(context.Context, any) ([]Hit, error) {
		return []Hit{{Track: Track{ID: "result"}, Evidence: Evidence{Channel: MERTChannel, Rank: 1}}}, nil
	}
	var observations []Evidence
	for _, id := range []string{"seed-a", "seed-b"} {
		result, err := executor.Query(context.Background(), Query{MERT: &NeighborQuery{SeedID: local.NamespacedID(id), Limit: 10}})
		if err != nil || len(result.Candidates) != 1 {
			t.Fatalf("query: %+v, %v", result, err)
		}
		observations = append(observations, result.Candidates[0].Evidence...)
	}
	if got := uniqueEvidence(observations); len(got) != 1 || got[0].QueryID != "seed-recording:"+identity {
		t.Fatalf("duplicate seed files counted independently: %+v", got)
	}
}
