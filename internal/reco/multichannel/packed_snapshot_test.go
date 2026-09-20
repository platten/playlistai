package multichannel

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestPackedSnapshotIdentityIncludesStoredEvidence(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		makeResult := func(score float64) core.Playlist {
			o := &Orchestrator{packedModel: core.AudioModelIdentity{Weights: "paired"}, packedAssessments: map[string]core.AudioAssessment{"track": {TrackID: "track", AnalysisID: "pack:generation", Clauses: []core.AudioClauseAssessment{{Score: score, ScoreAvailable: true}}}}}
			p := core.Playlist{Tracks: []core.TrackRef{{ID: "track"}}}
			if mixed {
				p.AudioEvidence = &core.AudioEvidenceSnapshot{ID: "preview", Assessments: []core.AudioAssessment{{TrackID: "track", AnalysisID: "preview"}}}
			}
			o.appendPackedEvidence(&p)
			return p
		}
		a, b, c := makeResult(.4), makeResult(.8), makeResult(.4)
		if a.AudioEvidence.ID == "" || a.AudioEvidence.ID == b.AudioEvidence.ID || a.AudioEvidence.ID != c.AudioEvidence.ID {
			t.Fatal("packed evidence identity missing or nondeterministic")
		}
		if len(a.AudioEvidence.LibraryAssessments) != 1 || a.AudioEvidence.LibraryModel == nil {
			t.Fatal("packed evidence lost in merge")
		}
		if mixed && a.AudioEvidence.Assessments[0].AnalysisID != "preview" {
			t.Fatal("preview evidence overwritten")
		}
	}
}
