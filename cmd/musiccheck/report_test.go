package main

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestGroundedMusicalEvidenceDoesNotAcceptGenericPadding(t *testing.T) {
	track := core.TrackRef{ID: "track", Artist: "Artist", Title: "Title"}
	playlist := core.Playlist{
		Tracks: []core.TrackRef{track},
		Rationale: []core.StepReason{{TrackID: track.ID, Evidence: []core.ComponentEvidence{
			{Component: "reciprocal_rank_fusion", Available: true, Score: 1},
			{Component: "listener_affinity", Available: true, Score: 1},
		}}},
	}
	if hasGroundedMusicalEvidence(playlist, track.ID) {
		t.Fatal("retrieval and taste padding counted as musical-fit evidence")
	}
	playlist.Rationale[0].Evidence = append(playlist.Rationale[0].Evidence, core.ComponentEvidence{Component: "library_metadata", Available: true, Score: .5})
	if !hasGroundedMusicalEvidence(playlist, track.ID) {
		t.Fatal("positive validated catalog comparison was ignored")
	}
}

func TestGroundedMusicalEvidenceTracksSourcesWithoutRelabeling(t *testing.T) {
	track := core.TrackRef{ID: "track"}
	playlist := core.Playlist{Tracks: []core.TrackRef{track}, AudioEvidence: &core.AudioEvidenceSnapshot{Assessments: []core.AudioAssessment{{TrackID: track.ID, AnalysisID: "analysis", Eligible: true}}}}
	if !hasGroundedMusicalEvidence(playlist, track.ID) {
		t.Fatal("eligible CLAP comparison was ignored")
	}
	r := result{Playlist: playlist}
	populateEvidenceReport(&r)
	if r.EvidenceCoverage.GroundedSelected != 1 || r.EvidenceCoverage.CLAPSelected != 1 || r.EvidenceCoverage.LibraryCLAPSelected != 0 {
		t.Fatalf("evidence sources were conflated: %+v", r.EvidenceCoverage)
	}
}
