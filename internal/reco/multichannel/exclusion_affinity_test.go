package multichannel

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestOutputArtistExclusionDoesNotBecomeNegativeSoundAffinity(t *testing.T) {
	intent := core.MusicIntent{
		References: []core.IntentReference{
			{Kind: core.ReferenceArtist, Query: "Aerosmith", Influence: core.InfluencePositive, TrackID: "seed"},
			{Kind: core.ReferenceArtist, Query: "Aerosmith", Influence: core.InfluenceNegative, TrackID: "seed"},
		},
		HardConstraints: []core.HardConstraint{{Kind: "exclude_artist", Value: "Aerosmith"}},
	}
	cat := testCatalog()
	if got := positiveReferenceVectors(cat, intent); len(got) != 1 {
		t.Fatalf("positive similarity anchor lost: %+v", got)
	}
	if got := negativeReferenceVectors(cat, intent); len(got) != 0 {
		t.Fatalf("output-only exclusion became negative sound affinity: %+v", got)
	}

	intent.HardConstraints = nil
	if got := negativeReferenceVectors(cat, intent); len(got) != 1 {
		t.Fatalf("independent negative sound reference lost: %+v", got)
	}
}
