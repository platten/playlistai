package core

import (
	"encoding/json"
	"testing"
)

func TestSearchSnapshotFreezesPoolEvidenceAndResult(t *testing.T) {
	pool := []Candidate{{Track: TrackRef{ID: "one"}}}
	result := Playlist{Tracks: []TrackRef{{ID: "one"}}, Seed: "18446744073709551615", AudioEvidence: &AudioEvidenceSnapshot{ID: "audio"}}
	frozen, err := FreezeSearch(SearchSnapshot{PolicyVersion: EnhancedSearchPolicyVersion, Candidates: pool, EligibleCandidates: pool}, result)
	if err != nil || frozen.Validate() != nil {
		t.Fatalf("freeze: %v validate: %v", err, frozen.Validate())
	}
	pool[0].Track.ID = "changed"
	result.AudioEvidence.ID = "changed"
	if frozen.Candidates[0].Track.ID != "one" || frozen.Result.AudioEvidence.ID != "audio" {
		t.Fatal("snapshot shared mutable evidence")
	}
	blob, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip SearchSnapshot
	if err := json.Unmarshal(blob, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Validate() != nil || roundTrip.Result.Seed != result.Seed {
		t.Fatal("round trip lost identity or seed")
	}
	roundTrip.Result.Tracks[0].ID = "tampered"
	if roundTrip.Validate() == nil {
		t.Fatal("changed result accepted as replay")
	}
}

func TestLegacySearchSnapshotRetainsUnknownEvidence(t *testing.T) {
	legacy, err := FreezeSearch(SearchSnapshot{PolicyVersion: "enhanced-search/v1", Candidates: []Candidate{{Track: TrackRef{ID: "old"}}}}, Playlist{Tracks: []TrackRef{{ID: "old"}}})
	if err != nil || legacy.Validate() != nil {
		t.Fatalf("legacy snapshot cannot replay: %v", err)
	}
	if legacy.Candidates[0].Criteria != nil || legacy.Candidates[0].Decision != "" || legacy.GenerationLimitMilliseconds != 0 {
		t.Fatal("legacy evidence or search budget was invented")
	}
	legacy.Candidates[0].Criteria = []CriterionAssessment{{State: EvidenceMatch}}
	if legacy.Validate() == nil {
		t.Fatal("new claims silently changed a frozen legacy assessment")
	}
}
