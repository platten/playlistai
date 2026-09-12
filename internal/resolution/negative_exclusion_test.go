package resolution

import (
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestResolvedArtistExclusionRetainsLiteralAndCatalogIdentity(t *testing.T) {
	for _, tc := range []struct{ literal, selected string }{
		{"christrian loeffler", "Christian Löffler"},
		{"Bjork", "Björk"},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			source := core.SourceEvidence{Text: "no " + tc.literal, Start: 0, End: len("no " + tc.literal), Explicit: true}
			input := core.MusicIntent{Version: core.CurrentIntentVersion,
				References:      []core.IntentReference{{Kind: core.ReferenceArtist, Query: tc.literal, Influence: core.InfluenceNegative, Evidence: []core.SourceEvidence{source}}},
				HardConstraints: []core.HardConstraint{{Kind: "exclude_artist", Value: tc.literal, Evidence: []core.SourceEvidence{source}}}}
			resolver := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "catalog-v2", Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: tc.selected, Representatives: []core.WeightedTrack{{TrackID: "catalog-recording", Weight: 1}}}}}
			got, issues := Apply(resolver, input)
			if len(issues) != 0 || !reflect.DeepEqual(got.Constraints.ArtistsExclude, []string{tc.literal, tc.selected}) {
				t.Fatalf("resolved exclusion identity lost: %+v issues=%+v", got.Constraints, issues)
			}
			if got.HardConstraints[0].Value != tc.literal || got.References[0].Query != tc.literal || !reflect.DeepEqual(got.HardConstraints[0].Evidence, []core.SourceEvidence{source}) || input.References[0].Resolution != nil {
				t.Fatal("resolution rewrote or mutated literal source evidence")
			}
			replayed := got.Normalized()
			if !reflect.DeepEqual(replayed.Constraints.ArtistsExclude, got.Constraints.ArtistsExclude) {
				t.Fatal("replay duplicated or lost canonical exclusion")
			}
		})
	}
}
