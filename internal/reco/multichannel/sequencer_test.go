package multichannel

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
)

func journeyCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "start", Display: "Start - Anchor", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "first", Display: "First - Bridge", Audio: []float32{.92, .38}, Track: []float32{.92, .38}},
		fakes.CatalogTrack{ID: "middle", Display: "Middle - Anchor", Audio: []float32{.7, .7}, Track: []float32{.7, .7}},
		fakes.CatalogTrack{ID: "second", Display: "Second - Bridge", Audio: []float32{.38, .92}, Track: []float32{.38, .92}},
		fakes.CatalogTrack{ID: "end", Display: "End - Anchor", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
}

func TestJourneySequencingPreservesWaypointPlacementCountAndDeterminism(t *testing.T) {
	t.Parallel()
	cat := journeyCatalog()
	required := refs(cat, "start", "middle", "end")
	candidates := []core.Candidate{
		sequencingCandidate(cat, "second", .85), sequencingCandidate(cat, "first", .9),
	}
	intent := journeyIntent(5)
	request := ports.SequenceRequest{
		Intent: intent, Candidates: candidates, Required: required, Waypoints: required,
		ReferenceAnchors: required, Trajectory: NewWaypointTrajectory(cat, required), Seed: 9,
	}
	sequencer := NewSequencer(cat, DefaultConfig())
	first, err := sequencer.Sequence(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sequencer.Sequence(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("sequencing is not deterministic:\n%+v\n%+v", first, second)
	}
	if got := trackIDs(first.Tracks); got != "start,first,middle,second,end" {
		t.Fatalf("journey order = %s", got)
	}
	if len(first.Tracks) != intent.Count {
		t.Fatalf("journey count = %d, want %d", len(first.Tracks), intent.Count)
	}
}

func TestJourneyRequiredWaypointsFollowWaypointOrder(t *testing.T) {
	t.Parallel()
	cat := journeyCatalog()
	waypoints := refs(cat, "start", "middle", "end")
	required := refs(cat, "end", "start", "middle")
	if got := trackIDs(orderRequiredByWaypoints(required, waypoints)); got != "start,middle,end" {
		t.Fatalf("required waypoint order = %s", got)
	}
}

func TestNewOrderingMatchesOrImprovesBaselineWalkOnControlledJourney(t *testing.T) {
	t.Parallel()
	cat := journeyCatalog()
	intent := journeyIntent(5)
	newPlaylist, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := deejai.New(cat, fakes.NewSimilarityEngine(cat), cat).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if trackIDsAt(newPlaylist.Tracks, 0, 2, 4) != "start,middle,end" {
		t.Fatalf("new journey lost required waypoint order: %s", trackIDs(newPlaylist.Tracks))
	}
	newScore := transitionScore(cat, newPlaylist.Tracks, intent)
	baselineScore := transitionScore(cat, baseline.Tracks, intent)
	if newScore+1e-6 < baselineScore {
		t.Fatalf("new transition score %.4f below deejai/v4 baseline %.4f\nnew=%s\nbaseline=%s",
			newScore, baselineScore, trackIDs(newPlaylist.Tracks), trackIDs(baseline.Tracks))
	}
}

func TestHardArtistSpacingIsNeverRelaxed(t *testing.T) {
	t.Parallel()
	cat := diversityCatalog()
	intent := testIntent(2)
	intent.Constraints.NoRepeatArtistBackToBack = true
	intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "no_back_to_back_artist", Value: "true", Supported: true})
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{
		Intent: intent, Candidates: []core.Candidate{
			sequencingCandidate(cat, "a1", 1), sequencingCandidate(cat, "a2", .9),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 1 || len(result.Notices) == 0 || result.Notices[0].Code != "hard_artist_spacing_exhausted" {
		t.Fatalf("hard spacing was relaxed or not explained: %+v", result)
	}
}

func TestSoftArtistSpacingRelaxationIsExplicit(t *testing.T) {
	t.Parallel()
	cat := diversityCatalog()
	intent := testIntent(2)
	intent.Controls.ArtistDiversity = 1
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{
		Intent: intent, Candidates: []core.Candidate{
			sequencingCandidate(cat, "a1", 1), sequencingCandidate(cat, "a2", .9),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 2 || len(result.Notices) == 0 || result.Notices[0].Code != "soft_artist_spacing_relaxed" {
		t.Fatalf("soft spacing relaxation was not explicit: %+v", result)
	}
}

func journeyIntent(count int) core.MusicIntent {
	waypoints := []core.IntentReference{
		{Kind: core.ReferenceTrack, TrackID: "start", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, TrackID: "middle", Influence: core.InfluencePositive},
		{Kind: core.ReferenceTrack, TrackID: "end", Influence: core.InfluencePositive},
	}
	return core.MusicIntent{
		Version: core.CurrentIntentVersion, References: append([]core.IntentReference(nil), waypoints...),
		RequiredTracks: append([]core.IntentReference(nil), waypoints...),
		Journey:        core.JourneyPlan{Waypoints: append([]core.IntentReference(nil), waypoints...)},
		Mode:           core.ModeJourney, Controls: core.IntentControls{
			TotalTrackCount: count, AudioWeight: .5, CooccurrenceWeight: .5,
			TransitionSmoothness: 1,
		}, Seed: "9",
	}.Normalized()
}

func sequencingCandidate(cat ports.Catalog, id string, relevance float64) core.Candidate {
	meta, _ := cat.Meta(id)
	return core.Candidate{
		Track:     meta.Ref,
		Scores:    core.CandidateScores{Total: relevance, SelectionRelevance: relevance, MMR: relevance},
		Available: core.CandidateFeatures{SelectionRelevance: true, MMR: true},
		Sources:   []core.RetrievalEvidence{},
	}
}

func refs(cat ports.Catalog, ids ...string) []core.TrackRef {
	result := make([]core.TrackRef, 0, len(ids))
	for _, id := range ids {
		meta, _ := cat.Meta(id)
		result = append(result, meta.Ref)
	}
	return result
}

func trackIDs(tracks []core.TrackRef) string {
	ids := make([]string, len(tracks))
	for index, track := range tracks {
		ids[index] = track.ID
	}
	return strings.Join(ids, ",")
}

func trackIDsAt(tracks []core.TrackRef, positions ...int) string {
	selected := make([]core.TrackRef, 0, len(positions))
	for _, position := range positions {
		if position < len(tracks) {
			selected = append(selected, tracks[position])
		}
	}
	return trackIDs(selected)
}

func transitionScore(cat ports.Catalog, tracks []core.TrackRef, intent core.MusicIntent) float64 {
	var score float64
	for index := 1; index < len(tracks); index++ {
		left, _ := cat.Vectors(tracks[index-1].ID)
		right, _ := cat.Vectors(tracks[index].ID)
		similarity, _ := weightedVectorSimilarity(left, right, intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight)
		score += similarity
	}
	return score
}
