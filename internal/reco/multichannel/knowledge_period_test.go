package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestJourneyDatesConstrainMembershipAndPlacement(t *testing.T) {
	for _, best := range []bool{false, true} {
		for _, scope := range []string{"journey_start", "journey_end"} {
			for _, basis := range []string{"composition", "original_release"} {
				t.Run(scope+"/"+basis+map[bool]string{true: "/best", false: "/verified"}[best], func(t *testing.T) {
					o := &Orchestrator{bestAvailable: best, cfg: DefaultConfig(), knowledge: &core.KnowledgeSnapshot{
						Tracks: []core.EnrichedTrack{
							{Ref: core.TrackRef{ID: "inside"}, CompositionStartYear: 1950, CompositionEndYear: 1950, OriginalReleaseDate: "1950-01-01"},
							{Ref: core.TrackRef{ID: "outside"}, CompositionStartYear: 2010, CompositionEndYear: 2010, OriginalReleaseDate: "2010-01-01"},
						},
					}}
					intent := core.MusicIntent{Mode: core.ModeJourney, Count: 2, Temporal: []core.TemporalRequirement{{Basis: basis, Scope: scope, StartYear: 1901, EndYear: 2000}}}
					candidates := []core.Candidate{
						{Track: core.TrackRef{ID: "outside", Artist: "A", Title: "Outside"}, Scores: core.CandidateScores{Total: 1}},
						{Track: core.TrackRef{ID: "inside", Artist: "B", Title: "Inside"}, Scores: core.CandidateScores{Total: 1}},
					}
					stages, err := o.categoryMembership(context.Background(), candidates, intent)
					if err != nil || len(stages) != 2 {
						t.Fatalf("stages=%v err=%v", stages, err)
					}
					restricted := 0
					if scope == "journey_end" {
						restricted = 1
					}
					if stages[restricted]["outside"] || !stages[restricted]["inside"] || !stages[1-restricted]["outside"] {
						t.Fatalf("membership=%v", stages)
					}
					result, err := NewSequencer(testCatalog(), DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: candidates, CategoryStages: stages})
					if err != nil || len(result.Tracks) != 2 || result.Tracks[restricted].ID != "inside" {
						t.Fatalf("sequence=%+v err=%v", result, err)
					}
					reserved, _, _, err := o.reserveJourneyStages(context.Background(), candidates, nil, intent)
					if err != nil {
						t.Fatal(err)
					}
					if scope == "journey_start" && (len(reserved) == 0 || reserved[0].Track.ID != "inside") {
						t.Fatalf("wrong start reservation: %+v", reserved)
					}
					if !o.stageDateEligible("unknown", scope, intent) {
						t.Fatal("unknown date treated as contradiction")
					}
				})
			}
		}
	}
}

func TestResolvedAlbumMembershipStillFiltersMetadata(t *testing.T) {
	o := &Orchestrator{}
	intent := core.MusicIntent{
		HardConstraints: []core.HardConstraint{{Kind: "require_album", Value: "Chosen album"}},
		References: []core.IntentReference{{Kind: core.ReferenceAlbum, Query: "Chosen album", Resolution: &core.ReferenceResolution{
			Status:   core.ResolutionResolved,
			Selected: &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "member", Weight: 1}}},
		}}},
	}
	if !o.metadataEligible(core.TrackRef{ID: "member"}, intent) || o.metadataEligible(core.TrackRef{ID: "other"}, intent) {
		t.Fatal("resolved album membership not enforced")
	}
	intent.References = nil
	if o.metadataEligible(core.TrackRef{ID: "member"}, intent) {
		t.Fatal("unresolved album accepted")
	}
}
