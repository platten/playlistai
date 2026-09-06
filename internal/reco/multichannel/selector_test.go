package multichannel

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func diversityCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a1", Display: "Artist A - First", Audio: []float32{1, 0}, Track: []float32{1, 0}, Album: "Album A", AlbumReliable: true},
		fakes.CatalogTrack{ID: "a2", Display: "Artist A - Second", Audio: []float32{.99, .01}, Track: []float32{.99, .01}, Album: "Album A", AlbumReliable: true},
		fakes.CatalogTrack{ID: "b", Display: "Artist B - Different", Audio: []float32{0, 1}, Track: []float32{0, 1}, Album: "Album B", AlbumReliable: true},
		fakes.CatalogTrack{ID: "low", Display: "Artist C - Low", Audio: []float32{-.7, .7}, Track: []float32{-.7, .7}},
		fakes.CatalogTrack{ID: "unknown", Display: "Artist D - Unknown", Audio: []float32{.5, .5}, Track: []float32{.5, .5}, Album: "Unverified", AlbumReliable: false},
	)
}

func TestMMRSelectionBalancesRelevanceAndArtistConcentration(t *testing.T) {
	t.Parallel()
	cat := diversityCatalog()
	candidates := []core.Candidate{
		selectionCandidate(cat, "a1", 1), selectionCandidate(cat, "a2", .93),
		selectionCandidate(cat, "b", .85), selectionCandidate(cat, "low", .1),
	}
	cfg := DefaultConfig()
	selector := NewSelector(cat, cfg)
	intent := testIntent(2)
	intent.Controls.ArtistDiversity = 1
	diverse, err := selector.Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(diverse.Candidates); got != "a1,b" {
		t.Fatalf("diverse MMR selection = %s, want a1,b", got)
	}
	if diverse.Candidates[0].Scores.Total != 1 {
		t.Fatal("highest-relevance candidate was not preserved")
	}

	intent.Controls.ArtistDiversity = 0
	relevanceOnly, err := selector.Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(relevanceOnly.Candidates); got != "a1,a2" {
		t.Fatalf("relevance-only selection = %s, want a1,a2", got)
	}
}

func TestMMRRelevanceFloorReturnsStructuredPartial(t *testing.T) {
	t.Parallel()
	cat := diversityCatalog()
	result, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), []core.Candidate{
		selectionCandidate(cat, "a1", 1), selectionCandidate(cat, "low", -.5),
	}, ports.SelectionRequest{Intent: testIntent(2), Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || len(result.Notices) != 1 || result.Notices[0].Code != "selection_relevance_floor_exhausted" {
		t.Fatalf("relevance-floor result = %+v", result)
	}
}

func TestAlbumConcentrationUsesOnlyReliableMetadata(t *testing.T) {
	t.Parallel()
	cat := diversityCatalog()
	selector := NewSelector(cat, DefaultConfig())
	a1, _ := cat.Meta("a1")
	a2, _ := cat.Meta("a2")
	unknown, _ := cat.Meta("unknown")
	if score, available := selector.albumConcentration(a2.Ref, []core.TrackRef{a1.Ref}); !available || score != 1 {
		t.Fatalf("reliable album concentration = %v/%v", score, available)
	}
	if score, available := selector.albumConcentration(unknown.Ref, []core.TrackRef{a1.Ref}); available || score != 0 {
		t.Fatalf("unknown album was treated as measured: %v/%v", score, available)
	}
}

func selectionCandidate(cat ports.Catalog, id string, relevance float64) core.Candidate {
	meta, _ := cat.Meta(id)
	return core.Candidate{Track: meta.Ref, Scores: core.CandidateScores{Total: relevance}}
}

func candidateIDs(candidates []core.Candidate) string {
	ids := make([]string, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.Track.ID
	}
	return strings.Join(ids, ",")
}
