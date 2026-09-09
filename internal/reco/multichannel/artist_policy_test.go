package multichannel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func genreDiversityIntent(count int) core.MusicIntent {
	intent := testIntent(count)
	intent.Preferences.Genres = []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}}
	intent.Controls.ArtistDiversity = 0 // genre policy must not depend on a parser's knob default
	return intent
}

func TestGenreSelectionBroadensArtistsWithoutLoweringRelevanceFloor(t *testing.T) {
	cat := diversityCatalog()
	candidates := []core.Candidate{selectionCandidate(cat, "a1", 1), selectionCandidate(cat, "a2", .99), selectionCandidate(cat, "b", .8), selectionCandidate(cat, "unknown", .79), selectionCandidate(cat, "low", -.5)}
	result, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: genreDiversityIntent(3), Count: 3})
	if err != nil || candidateIDs(result.Candidates) != "a1,b,unknown" {
		t.Fatal(candidateIDs(result.Candidates), err)
	}
	// Existing required tracks count toward artist concentration.
	result, err = NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates[1:], ports.SelectionRequest{Intent: genreDiversityIntent(2), Count: 1, Required: refs(cat, "a1")})
	if err != nil || candidateIDs(result.Candidates) != "b" {
		t.Fatal(candidateIDs(result.Candidates), err)
	}
	// Affirmative musical evidence still takes priority over unknown fit.
	intent := genreDiversityIntent(2)
	intent.VerificationPolicy = core.BestAvailable
	candidates[0].MusicalFit, candidates[1].MusicalFit = core.EvidenceMatch, core.EvidenceMatch
	result, err = NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || candidateIDs(result.Candidates) != "a1,a2" {
		t.Fatal("diversity displaced positive musical evidence", candidateIDs(result.Candidates), err)
	}
}

func TestGenreSequencingKeepsSeparatorsAndReferenceIsNotOutput(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a1", Display: "Artist A - One"}, fakes.CatalogTrack{ID: "a2", Display: " artist   a - Two"}, fakes.CatalogTrack{ID: "a3", Display: "ARTIST A - Three"},
		fakes.CatalogTrack{ID: "b", Display: "Artist B - Four"}, fakes.CatalogTrack{ID: "c", Display: "Artist C - Five"})
	request := ports.SequenceRequest{Intent: genreDiversityIntent(5), ReferenceAnchors: refs(cat, "a1"), Candidates: []core.Candidate{
		sequencingCandidate(cat, "b", 1), sequencingCandidate(cat, "c", 1), sequencingCandidate(cat, "a1", .7), sequencingCandidate(cat, "a2", .7), sequencingCandidate(cat, "a3", .7),
	}}
	seq := NewSequencer(cat, DefaultConfig())
	result, err := seq.Sequence(context.Background(), request)
	if err != nil || len(result.Tracks) != 5 {
		t.Fatal(result, err)
	}
	assertArtistSpacing(t, result.Tracks, core.TrackRef{})
	again, err := seq.Sequence(context.Background(), request)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatal("non-deterministic sequencing", err)
	}
	request.RecentSelections = refs(cat, "a1")
	result, err = seq.Sequence(context.Background(), request)
	if err != nil || len(result.Tracks) != 4 {
		t.Fatal("unsafe/needlessly short continuation", result, err)
	}
	assertArtistSpacing(t, result.Tracks, request.RecentSelections[0])
}

func TestGenreRequiredTracksAreSeparatedOrConflictExplicitly(t *testing.T) {
	cat := diversityCatalog()
	seq := NewSequencer(cat, DefaultConfig())
	request := ports.SequenceRequest{Intent: genreDiversityIntent(3), Required: refs(cat, "a1", "a2"), ReferenceAnchors: refs(cat, "a1"), Candidates: []core.Candidate{sequencingCandidate(cat, "b", 1)}}
	result, err := seq.Sequence(context.Background(), request)
	if err != nil || trackIDs(result.Tracks) != "a1,b,a2" {
		t.Fatal(result, err)
	}
	request.Candidates = nil
	if _, err := seq.Sequence(context.Background(), request); !errors.Is(err, core.ErrRequiredTrackConflict) {
		t.Fatal("required conflict silently relaxed", err)
	}
}

func TestGenreBuildBalancesArtistsAndReturnsSafePartial(t *testing.T) {
	for _, artistCount := range []int{1, 3} {
		var tracks []fakes.CatalogTrack
		features := map[string]core.TrackFeatures{}
		var hits []core.SemanticHit
		for i := 0; i < 12; i++ {
			id := fmt.Sprint(i)
			artist := 0
			if artistCount > 1 && i >= 8 {
				artist = 1 + (i % 2)
			}
			tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: fmt.Sprintf("Artist %d - Song %d", artist, i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
			features[id] = completeStyleFeature(id, "electronic")
			hits = append(hits, core.SemanticHit{TrackID: id, Score: .9})
		}
		// A different artist in the wrong genre must never be used as a separator.
		tracks = append(tracks, fakes.CatalogTrack{ID: "rock", Display: "Rock Artist - Wrong Category", Audio: []float32{1, 0}, Track: []float32{1, 0}})
		features["rock"] = completeStyleFeature("rock", "rock")
		hits = append(hits, core.SemanticHit{TrackID: "rock", Score: 1})
		cat := fakes.NewCatalog(2, tracks...)
		sem := &semanticFixture{info: core.FeatureStoreInfo{SchemaVersion: 3, CatalogVersion: cat.CatalogVersion(), SupportedFacets: []string{"styles"}}, features: features, positive: hits}
		intent := core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Seed: "42", Mode: core.ModeSimilar, EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Scope: "playlist", Value: "electronic"}}, Controls: core.IntentControls{TotalTrackCount: 6, AudioWeight: .5, CooccurrenceWeight: .5, Discovery: 1, TransitionSmoothness: 1}}
		engine := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, sem, sem, DefaultConfig())
		playlist, err := engine.Build(context.Background(), intent)
		if err != nil {
			t.Fatal(err)
		}
		want := 6
		if artistCount == 1 {
			want = 1
			if playlist.Outcome.State != core.OutcomePartial {
				t.Fatal(playlist.Outcome)
			}
		}
		if len(playlist.Tracks) != want {
			t.Fatal(artistCount, playlist)
		}
		assertArtistSpacing(t, playlist.Tracks, core.TrackRef{})
		counts := map[string]int{}
		for _, track := range playlist.Tracks {
			if track.ID == "rock" {
				t.Fatal("musical eligibility bypassed")
			}
			counts[track.Artist]++
		}
		if artistCount == 3 {
			for _, count := range counts {
				if count != 2 {
					t.Fatal("genre selection dominated by one artist", counts)
				}
			}
		}
		again, err := engine.Build(context.Background(), playlist.Intent)
		if err != nil || !reflect.DeepEqual(playlist.Tracks, again.Tracks) {
			t.Fatal("replay changed", err)
		}
	}
}

func assertArtistSpacing(t *testing.T, tracks []core.TrackRef, previous core.TrackRef) {
	t.Helper()
	for _, track := range tracks {
		if previous.ID != "" && sameArtist(previous, track) {
			t.Fatal("back-to-back artist", previous, track)
		}
		previous = track
	}
}

func TestGenreSpacingRetainsMaximumUnfixedTail(t *testing.T) {
	for a := 0; a <= 4; a++ {
		for b := 0; b <= 3; b++ {
			for c := 0; c <= 2; c++ {
				counts := []int{a, b, c}
				var rows []fakes.CatalogTrack
				for artist, count := range counts {
					for i := 0; i < count; i++ {
						rows = append(rows, fakes.CatalogTrack{ID: fmt.Sprintf("%d-%d", artist, i), Display: fmt.Sprintf("Artist %d - Song %d", artist, i)})
					}
				}
				if len(rows) == 0 {
					continue
				}
				cat := fakes.NewCatalog(2, rows...)
				var candidates []core.Candidate
				for i, row := range rows {
					candidates = append(candidates, sequencingCandidate(cat, row.ID, float64(i+1)/float64(len(rows))))
				}
				for previousArtist := -1; previousArtist < 3; previousArtist++ {
					request := ports.SequenceRequest{Intent: genreDiversityIntent(len(rows)), Candidates: candidates}
					previous := core.TrackRef{}
					if previousArtist >= 0 {
						previous = core.TrackRef{ID: "previous", Artist: fmt.Sprintf("Artist %d", previousArtist)}
						request.RecentSelections = []core.TrackRef{previous}
					}
					excess := 0
					for artist, count := range counts {
						bonus := 1
						if artist == previousArtist {
							bonus = 0
						}
						excess = max(excess, 2*count-len(rows)-bonus)
					}
					result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
					if err != nil || len(result.Tracks) != len(rows)-excess {
						t.Fatalf("counts=%v previous=%d: got %d want %d: %v", counts, previousArtist, len(result.Tracks), len(rows)-excess, err)
					}
					assertArtistSpacing(t, result.Tracks, previous)
				}
			}
		}
	}
}
