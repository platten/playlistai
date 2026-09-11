package multichannel

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestIndexedCategorySearchMatchesFrozenReference(t *testing.T) {
	rng := rand.New(rand.NewSource(2181))
	for scenario := 0; scenario < 240; scenario++ {
		size := 2 + rng.Intn(10)
		tracks := make([]fakes.CatalogTrack, size)
		for index := range tracks {
			vector := []float32{1, 0}
			if scenario%3 != 0 { // every third scenario deliberately ties all scores
				vector = []float32{float32(rng.Float64()), float32(rng.Float64())}
			}
			tracks[index] = fakes.CatalogTrack{ID: fmt.Sprintf("t%02d", index), Display: fmt.Sprintf("Artist %d - Song %d", index%3, index), Audio: vector, Track: vector}
		}
		if scenario%4 == 0 {
			tracks[size-1].Display = tracks[0].Display // alternate ID, same recording
		}
		cat := fakes.NewCatalog(2, tracks...)
		request := ports.SequenceRequest{Intent: journeyIntent(1 + rng.Intn(size)), CategoryStages: make([]map[string]bool, rng.Intn(4))}
		request.Intent.Controls.TransitionSmoothness = float64(scenario%3) / 2
		request.Intent.Controls.ArtistDiversity = float64(scenario%4) / 3
		request.Intent.Constraints.NoRepeatArtistBackToBack = scenario%2 == 0
		for index := range request.CategoryStages {
			request.CategoryStages[index] = map[string]bool{}
		}
		for index, track := range tracks {
			if index < scenario%3 {
				request.Required = append(request.Required, refs(cat, track.ID)...)
			} else {
				request.Candidates = append(request.Candidates, sequencingCandidate(cat, track.ID, .9))
			}
			for _, membership := range request.CategoryStages {
				membership[track.ID] = rng.Intn(3) != 0
			}
		}
		request.Waypoints = append([]core.TrackRef(nil), request.Required...)
		if scenario%5 == 0 && len(request.Required) > 0 {
			request.Intent.Destination = &core.IntentReference{Kind: core.ReferenceTrack, TrackID: request.Required[len(request.Required)-1].ID}
		}
		if scenario%7 == 0 {
			request.RecentSelections = refs(cat, tracks[size-1].ID)
		} else {
			request.ReferenceAnchors = refs(cat, tracks[0].ID)
		}
		if scenario%11 == 0 {
			request.Trajectory = NewWaypointTrajectory(cat, refs(cat, tracks[0].ID, tracks[size-1].ID))
		}
		sequencer := NewSequencer(cat, DefaultConfig())
		got, exhausted, err := sequencer.categoryJourney(context.Background(), request)
		want, wantExhausted, wantErr := sequencer.referenceCategoryJourney(context.Background(), request)
		if fmt.Sprint(err) != fmt.Sprint(wantErr) || exhausted != wantExhausted || !reflect.DeepEqual(got, want) {
			t.Fatalf("scenario %d changed path, ties or exhaustion:\ngot=%+v exhausted=%v err=%v\nwant=%+v exhausted=%v err=%v", scenario, got, exhausted, err, want, wantExhausted, wantErr)
		}
	}
}

func TestIndexedCategorySearchMaximumCountMatchesReference(t *testing.T) {
	cat, request := benchmarkCategoryRequest()
	sequencer := NewSequencer(cat, DefaultConfig())
	got, exhausted, err := sequencer.categoryJourney(context.Background(), request)
	want, wantExhausted, wantErr := sequencer.referenceCategoryJourney(context.Background(), request)
	if err != nil || wantErr != nil || exhausted != wantExhausted || !reflect.DeepEqual(got, want) {
		t.Fatalf("maximum-count search changed: count=%d/%d exhausted=%v/%v err=%v/%v", len(got), len(want), exhausted, wantExhausted, err, wantErr)
	}
}
