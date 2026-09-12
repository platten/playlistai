package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestCorrectedNegativeArtistIsExcludedFromCandidatePool(t *testing.T) {
	for _, tc := range []struct{ literal, canonical string }{{"christrian loeffler", "Christian Löffler"}, {"Bjork", "Björk"}} {
		t.Run(tc.literal, func(t *testing.T) {
			intent := (core.MusicIntent{Version: core.CurrentIntentVersion,
				References:      []core.IntentReference{{Kind: core.ReferenceArtist, Query: tc.literal, Influence: core.InfluenceNegative, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: tc.canonical}}}},
				HardConstraints: []core.HardConstraint{{Kind: "exclude_artist", Value: tc.literal}}}).Normalized()
			blocked := core.TrackRef{ID: "excluded", Artist: tc.canonical, Title: "Known recording"}
			allowed := core.TrackRef{ID: "allowed", Artist: "Another Artist", Title: "Another recording"}
			policy := newEligibility(intent, nil, nil)
			got, err := policy.filter(context.Background(), []core.Candidate{{Track: blocked}, {Track: allowed}}, false)
			if err != nil || len(got) != 1 || got[0].Track.ID != allowed.ID {
				t.Fatalf("catalog identity escaped exclusion: %+v %v", got, err)
			}
			if err := policy.validateRequired([]core.TrackRef{blocked}, false); !errors.Is(err, core.ErrRequiredTrackConflict) {
				t.Fatalf("required recording escaped corrected exclusion: %v", err)
			}
		})
	}
}

func TestCorrectedArtistExclusionBlocksBothJourneyEndpoints(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	for _, role := range []string{"start", "destination"} {
		t.Run(role, func(t *testing.T) {
			intent := enhancedIntent(5)
			intent.Mode = core.ModeJourney
			endpoint := &core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}
			if role == "start" {
				intent.Start = endpoint
			} else {
				intent.Destination = endpoint
			}
			source := []core.SourceEvidence{{Text: "no Sead Artist", Start: 0, End: 14, Explicit: true}}
			intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: "Sead Artist", Influence: core.InfluenceNegative, Evidence: source, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: cat.CatalogVersion(), Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: "Seed Artist", Representatives: []core.WeightedTrack{{TrackID: "seed", Weight: 1}}}}})
			intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Sead Artist", Evidence: source}}
			result, err := engine.Build(context.Background(), intent)
			if err == nil && (len(result.Tracks) != 0 || result.Outcome.State != core.OutcomeNeedsClarification) {
				t.Fatalf("corrected exclusion bypassed required %s: %+v", role, result)
			}
		})
	}
}
