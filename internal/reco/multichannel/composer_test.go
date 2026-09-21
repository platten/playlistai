package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

type composerEvidenceCatalog struct{ *fakes.Catalog }

func (c composerEvidenceCatalog) CriterionEvidence(_ context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if criterion.Kind != "composer" || criterion.Value != "Fryderyk Chopin" {
		return core.EvidenceUnknown
	}
	switch id {
	case "chopin":
		return core.EvidenceMatch
	case "other":
		return core.EvidenceMismatch
	default:
		return core.EvidenceUnknown
	}
}

func TestComposerRestrictionNeverAdmitsPerformerOrUnknownCredits(t *testing.T) {
	cat := composerEvidenceCatalog{fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "chopin", Display: "Pianist A - Nocturne", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "other", Display: "Fryderyk Chopin - Beethoven Piece", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "unknown", Display: "Pianist B - Chopin Tribute", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)}
	criterion := core.MusicalCriterion{Kind: "composer", Value: "Fryderyk Chopin", Scope: "playlist", Strength: "required"}
	candidates := candidatesForTracks(refs(cat, "chopin", "other", "unknown"))
	for _, enhanced := range []bool{false, true} {
		for _, bestAvailable := range []bool{false, true} {
			o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
			o.enhanced, o.bestAvailable = enhanced, bestAvailable
			got, report, err := o.filterEssential(context.Background(), candidates, []core.MusicalCriterion{criterion})
			if err != nil || len(got) != 1 || got[0].Track.ID != "chopin" || !report.Eligible["chopin"] {
				t.Fatalf("enhanced=%v best=%v admitted unverified composer: %+v report=%+v err=%v", enhanced, bestAvailable, got, report, err)
			}
			unverified, _, err := o.filterEssential(context.Background(), candidates[1:], []core.MusicalCriterion{criterion})
			if err != nil || len(unverified) != 0 {
				t.Fatalf("enhanced=%v best=%v padded a missing composer credit: %+v err=%v", enhanced, bestAvailable, unverified, err)
			}
		}
	}
}
