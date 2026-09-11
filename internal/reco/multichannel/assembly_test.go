package multichannel

import (
	"context"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestCompletionAndFinalAssemblyUseCanonicalRecentContext(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := testIntent(1)
	intent.HardConstraints = []core.HardConstraint{{Kind: "no_back_to_back_artist", Value: "true"}}
	intent = intent.Normalized()
	candidates := candidatesForTracks(refs(cat, "audio-copy"))
	// The original caller supplied only an ID. Its artist must be resolved for
	// completion just as it is for the final output boundary.
	request := ports.RecommendationRequest{Intent: intent, RecentSelections: []core.TrackRef{{ID: "audio"}}}
	complete, err := engine.iterativeComplete(context.Background(), candidates, intent, request, nil, nil, nil, 42)
	if err != nil || complete {
		t.Fatalf("raw recent context prematurely completed: %v %v", complete, err)
	}
	assembly, err := engine.assembleCandidates(context.Background(), candidates, intent, request, nil, nil, nil, 42)
	if err != nil || assembly.complete(intent.Count) || len(assembly.sequence.Tracks) != 0 {
		t.Fatalf("final assembly disagrees: %+v %v", assembly, err)
	}
	request.RecentSelections = refs(cat, "audio")
	canonical, err := engine.assembleCandidates(context.Background(), candidates, intent, request, nil, nil, nil, 42)
	if err != nil || !reflect.DeepEqual(assembly, canonical) {
		t.Fatal("assembly depends on stale/missing recent-track metadata", err)
	}
}

func TestAssemblyCompletenessDoesNotUpgradePartialJourney(t *testing.T) {
	for _, a := range []candidateAssembly{
		{countConflict: true},
		{sequence: ports.SequenceResult{Tracks: []core.TrackRef{{ID: "a"}}}, reserveReasons: []core.OutcomeReason{{Code: "missing_stage"}}},
		{sequence: ports.SequenceResult{Tracks: []core.TrackRef{{ID: "a"}}, Notices: []core.PlaylistNotice{{Code: "category_journey_exhausted"}}}},
		{sequence: ports.SequenceResult{}},
	} {
		if a.complete(1) {
			t.Fatalf("incomplete assembly reported complete: %+v", a)
		}
	}
	if !(candidateAssembly{sequence: ports.SequenceResult{Tracks: []core.TrackRef{{ID: "a"}}}}).complete(1) {
		t.Fatal("valid full-count assembly was rejected")
	}
}
