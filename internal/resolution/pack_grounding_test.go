package resolution

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type packGroundingResolver struct{ received string }

func (*packGroundingResolver) CatalogVersion() string { return "pack-test" }
func (r *packGroundingResolver) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	r.received = ref.TrackID
	return core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceTrack, EntityID: ref.TrackID, Artist: "Artist", Title: "Track", Representatives: []core.WeightedTrack{{TrackID: ref.TrackID, Weight: 1}}}}
}
func TestPackGroundingRetainsCatalogNamespace(t *testing.T) {
	for _, id := range []string{"local:test:recording", "pack:test:recording", "musicbrainz:recording"} {
		r := &packGroundingResolver{}
		refs, issues := applyList(r, []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Artist Track", Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{ID: id, Kind: core.ReferenceTrack, Name: "Artist", Title: "Track"}}}}}, false, nil)
		if r.received != id || len(issues) != 0 || refs[0].TrackID != id {
			t.Fatalf("namespaced recording was rewritten: %s %+v", r.received, issues)
		}
	}
	if !groundingMatchesSelected(core.IdentityCandidate{Kind: core.ReferenceAlbum, Name: "Artist", Title: "Album"}, core.ResolutionCandidate{Kind: core.ReferenceAlbum, Artist: "Artist", Title: "Album"}) {
		t.Fatal("album title not grounded")
	}
	if groundingMatchesSelected(core.IdentityCandidate{Kind: core.ReferenceAlbum, Name: "Artist", Title: "Different"}, core.ResolutionCandidate{Kind: core.ReferenceAlbum, Artist: "Artist", Title: "Album"}) {
		t.Fatal("artist alone matched different album")
	}
}
