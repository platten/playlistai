package deejai_test

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/reco/deejai"
)

func TestRequiredArtistSpacingUsesSeparatorsOrReportsConflict(t *testing.T) {
	for _, mode := range []core.Mode{core.ModeSimilar, core.ModeJourney} {
		for _, count := range []int{2, 3} {
			t.Run(string(mode)+itoa(count), func(t *testing.T) {
				engine, _ := fakeEngine(t, 20, 5, 12)
				intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: mode, Seed: "1",
					RequiredTracks:  []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "trk0"}, {Kind: core.ReferenceTrack, TrackID: "trk5"}},
					HardConstraints: []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}},
					Controls:        core.IntentControls{TotalTrackCount: count, AudioWeight: .5, CooccurrenceWeight: .5},
				}
				got, err := deejai.BuildOnly(context.Background(), engine, intent)
				if count == 2 {
					if !errors.Is(err, core.ErrRequiredTrackConflict) || len(got.Tracks) != 0 {
						t.Fatalf("impossible hard spacing: tracks=%+v err=%v", got.Tracks, err)
					}
					return
				}
				if err != nil || len(got.Tracks) != 3 || got.Outcome.State != core.OutcomeFulfilled {
					t.Fatalf("eligible separator not used: %+v err=%v", got, err)
				}
				if got.Tracks[0].ID != "trk0" || got.Tracks[2].ID != "trk5" || got.Tracks[1].Artist == "Artist A" {
					t.Fatalf("required order or hard spacing changed: %+v", got.Tracks)
				}
			})
		}
	}
}

func TestRequiredSeparatorCannotBypassArtistExclusions(t *testing.T) {
	engine, _ := fakeEngine(t, 10, 2, 7)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "1",
		RequiredTracks:  []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "trk0"}, {Kind: core.ReferenceTrack, TrackID: "trk2"}},
		HardConstraints: []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}, {Kind: "exclude_artist", Value: "Artist B"}},
		Controls:        core.IntentControls{TotalTrackCount: 3, AudioWeight: .5, CooccurrenceWeight: .5},
	}
	if _, err := engine.Build(context.Background(), intent); !errors.Is(err, core.ErrRequiredTrackConflict) {
		t.Fatalf("excluded separator accepted: %v", err)
	}
}

func TestJourneyChecksUpcomingRequiredArtist(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "0", Display: "Start - Required", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "1", Display: "End - Required", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "2", Display: "End - Closest", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "3", Display: "Separator - Valid", Audio: []float32{.9, .1}, Track: []float32{.9, .1}},
	)
	engine := deejai.New(cat, fakes.NewSimilarityEngine(cat))
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeJourney, Seed: "1",
		RequiredTracks:  []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "0"}, {Kind: core.ReferenceTrack, TrackID: "1"}},
		HardConstraints: []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}},
		Controls:        core.IntentControls{TotalTrackCount: 3, AudioWeight: .5, CooccurrenceWeight: .5},
	}
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 3 || got.Tracks[1].ID != "3" || got.Tracks[2].ID != "1" {
		t.Fatalf("journey endpoint adjacency: %+v, %v", got.Tracks, err)
	}
}

func TestReferenceAnchorIsNotPriorOutputForHardSpacing(t *testing.T) {
	engine, _ := fakeEngine(t, 10, 1, 12)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "1",
		References:      []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "trk0", Influence: core.InfluencePositive}},
		HardConstraints: []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}},
		Controls:        core.IntentControls{TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5},
	}
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 1 || got.Tracks[0].ID == "trk0" {
		t.Fatalf("reference was treated as an emitted track: %+v %v", got.Tracks, err)
	}
}

func TestJourneyReservesScarceSlotsForRequiredArtistConflicts(t *testing.T) {
	for _, artists := range [][]string{{"A", "B", "B"}, {"A", "A", "B"}, {"A", "A", "B", "B"}} {
		var tracks []fakes.CatalogTrack
		var required []core.IntentReference
		separators := 0
		for index, artist := range artists {
			id := itoa(index)
			tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: artist + " - Required " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
			required = append(required, core.IntentReference{Kind: core.ReferenceTrack, TrackID: id})
			if index > 0 && artist == artists[index-1] {
				separators++
			}
		}
		for index := range separators {
			tracks = append(tracks, fakes.CatalogTrack{ID: "separator" + itoa(index), Display: "C - Separator " + itoa(index), Audio: []float32{1, 0}, Track: []float32{1, 0}})
		}
		cat := fakes.NewCatalog(2, tracks...)
		intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeJourney, Seed: "1", RequiredTracks: required,
			HardConstraints: []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}},
			Controls:        core.IntentControls{TotalTrackCount: len(tracks), AudioWeight: .5, CooccurrenceWeight: .5},
		}
		got, err := deejai.New(cat, fakes.NewSimilarityEngine(cat)).Build(context.Background(), intent)
		if err != nil || len(got.Tracks) != len(tracks) {
			t.Fatalf("required artists %v: %+v %v", artists, got.Tracks, err)
		}
		nextRequired := 0
		for index, track := range got.Tracks {
			if index > 0 && got.Tracks[index-1].Artist == track.Artist {
				t.Fatalf("required artists %v violate adjacency: %+v", artists, got.Tracks)
			}
			if nextRequired < len(required) && track.ID == required[nextRequired].TrackID {
				nextRequired++
			}
		}
		if nextRequired != len(required) {
			t.Fatalf("required order lost: %+v", got.Tracks)
		}
	}
}
