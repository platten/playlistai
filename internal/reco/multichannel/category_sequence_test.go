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

func TestCategorySequencingPreservesViaAndRequiredOrder(t *testing.T) {
	cat := journeyCatalog()
	request := ports.SequenceRequest{
		Intent: journeyIntent(5), Required: refs(cat, "start", "middle", "end"),
		Candidates:     []core.Candidate{sequencingCandidate(cat, "first", .9), sequencingCandidate(cat, "second", .9)},
		CategoryStages: []map[string]bool{{"start": true, "first": true}, {"middle": true}, {"second": true, "end": true}},
		Waypoints:      refs(cat, "start", "middle", "end"),
	}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
	if err != nil || trackIDs(result.Tracks) != "start,first,middle,second,end" {
		t.Fatalf("via/required order lost: %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSequencer(cat, DefaultConfig()).Sequence(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("category sequencing ignored cancellation: %v", err)
	}
}

func TestCategorySequencingHybridNeedsDistinctTrackPerStage(t *testing.T) {
	cat := journeyCatalog()
	request := ports.SequenceRequest{
		Intent: journeyIntent(3), Required: refs(cat, "middle"),
		Candidates:     []core.Candidate{sequencingCandidate(cat, "first", .9), sequencingCandidate(cat, "second", .9)},
		CategoryStages: []map[string]bool{{"first": true, "middle": true}, {"middle": true, "second": true}},
	}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
	if err != nil || len(result.Tracks) != 3 {
		t.Fatalf("hybrid should permit full journey: %+v, %v", result, err)
	}
	if result.Tracks[0].ID != "first" || result.Tracks[2].ID != "second" {
		t.Fatalf("hybrid moved category-only tracks backwards: %+v", result.Tracks)
	}
	request.Candidates = nil
	result, err = NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
	if err != nil || len(result.Tracks) != 1 || len(result.Notices) == 0 || result.Notices[0].Code != "category_journey_exhausted" {
		t.Fatalf("one hybrid falsely fulfilled two distinct stages: %+v, %v", result, err)
	}
}

func benchmarkCategoryRequest() (*fakes.Catalog, ports.SequenceRequest) {
	tracks := make([]fakes.CatalogTrack, core.MaxCount)
	for index := range tracks {
		tracks[index] = fakes.CatalogTrack{ID: fmt.Sprintf("t%03d", index), Display: fmt.Sprintf("Artist %d - Recording", index), Audio: []float32{1, 0}, Track: []float32{1, 0}}
	}
	cat := fakes.NewCatalog(2, tracks...)
	request := ports.SequenceRequest{Intent: journeyIntent(core.MaxCount), CategoryStages: []map[string]bool{{}, {}}}
	for index, track := range tracks {
		request.Candidates = append(request.Candidates, sequencingCandidate(cat, track.ID, .9))
		request.CategoryStages[index/(core.MaxCount/2)][track.ID] = true
	}
	return cat, request
}

func BenchmarkCategoryJourney100Tracks(b *testing.B) {
	cat, request := benchmarkCategoryRequest()
	sequencer := NewSequencer(cat, DefaultConfig())
	b.ResetTimer()
	for b.Loop() {
		result, err := sequencer.Sequence(context.Background(), request)
		if err != nil || len(result.Tracks) != core.MaxCount {
			b.Fatalf("maximum-size category journey: count=%d, %v", len(result.Tracks), err)
		}
	}
}
