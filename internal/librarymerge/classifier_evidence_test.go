package librarymerge

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestMergePreservesClassifierObservationWithoutDuplicateVotes(t *testing.T) {
	evidence := core.MusicClassifierEvidence{Encoder: core.MusicClassifierIdentity{Model: "fixture"}, AudioSHA256: "source", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 10, Segments: []core.LibraryAudioInterval{{EndSeconds: 10}}}}
	first := librarypack.Track{DurationMilliseconds: 20000}
	later := librarypack.Track{ClassifierEvidence: []core.MusicClassifierEvidence{evidence}}
	var vector []float32
	conflicts := map[string]int{}
	mergeTrackEvidence(&first, &vector, later, nil, conflicts)
	mergeTrackEvidence(&first, &vector, later, nil, conflicts)
	if len(first.ClassifierEvidence) != 1 || len(conflicts) != 0 {
		t.Fatal("lost or duplicated observation", first.ClassifierEvidence, conflicts)
	}
	later.ClassifierEvidence[0].License = "changed terms"
	mergeTrackEvidence(&first, &vector, later, nil, conflicts)
	if conflicts["classifier_evidence"] != 1 || first.ClassifierEvidence[0].License != "" {
		t.Fatal("conflicting observation replaced first", conflicts)
	}
	later.ClassifierEvidence[0].AudioSHA256 = "another-source"
	later.ClassifierEvidence[0].Coverage.Segments = []core.LibraryAudioInterval{{EndSeconds: 30}}
	mergeTrackEvidence(&first, &vector, later, nil, conflicts)
	if len(first.ClassifierEvidence) != 1 || conflicts["classifier_evidence"] != 2 {
		t.Fatal("coverage beyond retained duration accepted")
	}
}
