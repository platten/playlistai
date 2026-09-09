package multichannel

import (
	"context"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func artistRequestCatalog() *fakes.Catalog {
	var tracks []fakes.CatalogTrack
	for i := 0; i < 70; i++ {
		tracks = append(tracks, fakes.CatalogTrack{ID: fmt.Sprint(i), Display: fmt.Sprintf("Artist %d - Track %d", i%5, i), Audio: []float32{1, float32(i%7) / 10}, Track: []float32{1, float32(i%5) / 10}})
	}
	return fakes.NewCatalog(2, tracks...)
}

func TestArtistSuggestionsAreDiverseAndBridgeReferences(t *testing.T) {
	cat := artistRequestCatalog()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, VerificationPolicy: core.BestAvailable, Seed: "42",
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist 0", Influence: core.InfluencePositive}, {Kind: core.ReferenceArtist, Query: "Artist 1", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{TotalTrackCount: 10, AudioWeight: .5, CooccurrenceWeight: .5, ArtistDiversity: .7, TransitionSmoothness: .7}}
	source := &fixtureDiscovery{tracks: refs(cat, "40")}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(source)
	proposals := 0
	engine.WithAnchorProposer(func(context.Context, core.MusicIntent, []string) ([]core.InferredAnchor, error) {
		proposals++
		return nil, nil
	})
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 10 {
		t.Fatalf("tracks=%d outcome=%+v err=%v", len(playlist.Tracks), playlist.Outcome, err)
	}
	if source.pulls != 0 || proposals != 0 {
		t.Fatal("named artists were displaced by metadata or model proposals")
	}
	artists := map[string]bool{}
	trajectory := false
	for i, track := range playlist.Tracks {
		artists[track.Artist] = true
		if i > 0 && track.Artist == playlist.Tracks[i-1].Artist {
			t.Fatal("back-to-back artist")
		}
		for _, evidence := range playlist.Rationale[i].Evidence {
			trajectory = trajectory || evidence.Component == "embedding_trajectory" && evidence.Available
		}
	}
	if len(artists) < 3 || !trajectory || len(playlist.Intent.RequiredTracks) != 0 {
		t.Fatalf("artists=%d trajectory=%v; references must not become required tracks", len(artists), trajectory)
	}
	again, err := engine.Build(context.Background(), playlist.Intent)
	if err != nil || trackIDs(again.Tracks) != trackIDs(playlist.Tracks) {
		t.Fatal("replay changed", err)
	}
}

func TestExplicitArtistOnlyProducesTenTracksWithoutOtherArtists(t *testing.T) {
	cat := artistRequestCatalog()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, VerificationPolicy: core.BestAvailable, Seed: "42",
		References:      []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist 0", Influence: core.InfluencePositive}},
		HardConstraints: []core.HardConstraint{{Kind: "require_artist", Value: "Artist 0"}},
		Controls:        core.IntentControls{TotalTrackCount: 10, AudioWeight: .5, CooccurrenceWeight: .5, ArtistDiversity: .7}}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 10 {
		t.Fatalf("tracks=%d outcome=%+v err=%v", len(playlist.Tracks), playlist.Outcome, err)
	}
	for _, track := range playlist.Tracks {
		if track.Artist != "Artist 0" {
			t.Fatal("artist-only filter bypassed")
		}
	}
	if !playlist.Intent.HardConstraints[0].RuntimeEnforced {
		t.Fatal("runtime enforcement not reported")
	}
	spacing := intent
	spacing.HardConstraints = append(append([]core.HardConstraint(nil), intent.HardConstraints...), core.HardConstraint{Kind: "no_back_to_back_artist", Value: "true"})
	conflict, err := engine.Build(context.Background(), spacing)
	if err != nil || len(conflict.Tracks) != 0 || conflict.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatalf("artist-only and hard spacing conflict hidden: %+v %v", conflict, err)
	}
	intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: "Artist 0"})
	conflict, err = engine.Build(context.Background(), intent)
	if err != nil || len(conflict.Tracks) != 0 || conflict.Outcome.State == core.OutcomeFulfilled {
		t.Fatalf("contradictory request padded with other artists: %+v %v", conflict, err)
	}
}

func TestDestinationDoesNotPromoteReferenceToRequiredOutput(t *testing.T) {
	cat := testCatalog()
	intent := testIntent(4)
	intent.VerificationPolicy = core.BestAvailable
	intent.Mode = core.ModeJourney
	intent.Constraints.ArtistsExclude = []string{"Seed Artist"}
	intent.Destination = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Last Artist", Influence: core.InfluencePositive}
	playlist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 4 {
		t.Fatalf("an excluded reference became mandatory: tracks=%d outcome=%+v err=%v", len(playlist.Tracks), playlist.Outcome, err)
	}
	if playlist.Tracks[3].Artist != "Last Artist" {
		t.Fatal("destination lost")
	}
	for _, track := range playlist.Tracks {
		if track.ID == "seed" {
			t.Fatal("reference exclusion bypassed")
		}
	}
}
