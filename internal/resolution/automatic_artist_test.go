package resolution

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAutomaticArtistDecisionRequiresSelectedIdentitySeed(t *testing.T) {
	u, v := int64(100), int64(1)
	g := &core.IdentityGrounding{Provider: "MusicBrainz", MatchedSpelling: "Shared Name", SnapshotVersion: "names", Candidates: []core.IdentityCandidate{
		{Kind: core.ReferenceArtist, ID: "popular", Name: "Shared Name", MatchType: "canonical", Popularity: &core.ArtistPopularity{Snapshot: "p", UniqueListeners: &u}},
		{Kind: core.ReferenceArtist, ID: "other", Name: "Shared Name", MatchType: "canonical", Popularity: &core.ArtistPopularity{Snapshot: "p", UniqueListeners: &v}},
	}}
	for _, tc := range []struct {
		name, id            string
		automatic, explicit bool
		want                core.ResolutionStatus
	}{
		{"selected provider ID", "popular", true, false, core.ResolutionResolved},
		{"wrong namesake", "other", true, false, core.ResolutionUnresolved},
		{"name-only catalog", "artist:shared name", true, false, core.ResolutionUnresolved},
		{"legacy ambiguity", "popular", false, false, core.ResolutionAmbiguous},
		{"explicit correction", "other", true, true, core.ResolutionResolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "catalog-v2", Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: tc.id, Artist: "Shared Name", Representatives: []core.WeightedTrack{{TrackID: "seed"}}}}}
			ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Shared Name", Grounding: g, Influence: core.InfluenceNegative}
			if tc.explicit {
				ref.TrackID = "chosen"
			}
			refs, _ := applyList(context.Background(), r, []core.IntentReference{ref}, true, nil, tc.automatic)
			if refs[0].Resolution.Status != tc.want || refs[0].Influence != core.InfluenceNegative || len(refs[0].Grounding.Candidates) != 2 {
				t.Fatalf("resolution: %+v", refs[0])
			}
			if g.Decision != nil {
				t.Fatal("source grounding mutated")
			}
		})
	}
}
