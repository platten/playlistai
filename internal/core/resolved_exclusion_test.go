package core

import "testing"

func TestResolvedNegativePreferenceNeedsMatchingHardArtistExclusion(t *testing.T) {
	for _, tc := range []struct {
		name, constraint, influence, status string
		kind                                ReferenceKind
		selectedKind                        ReferenceKind
		wantCanonical                       bool
	}{
		{"matched", "raw typo", "negative", "resolved", ReferenceArtist, ReferenceArtist, true},
		{"negative preference only", "", "negative", "resolved", ReferenceArtist, ReferenceArtist, false},
		{"other excluded artist", "Other Artist", "negative", "resolved", ReferenceArtist, ReferenceArtist, false},
		{"positive reference", "raw typo", "positive", "resolved", ReferenceArtist, ReferenceArtist, false},
		{"ambiguous selection", "raw typo", "negative", "ambiguous", ReferenceArtist, ReferenceArtist, false},
		{"unresolved selection", "raw typo", "negative", "unresolved", ReferenceArtist, ReferenceArtist, false},
		{"negative track", "raw typo", "negative", "resolved", ReferenceTrack, ReferenceTrack, false},
		{"mismatched namespace", "raw typo", "negative", "resolved", ReferenceArtist, ReferenceTrack, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := MusicIntent{Version: CurrentIntentVersion, References: []IntentReference{{Kind: tc.kind, Query: "raw typo", Influence: Influence(tc.influence), Resolution: &ReferenceResolution{Status: ResolutionStatus(tc.status), Selected: &ResolutionCandidate{Kind: tc.selectedKind, Artist: "Canonical Artist"}}}}}
			if tc.constraint != "" {
				intent.HardConstraints = []HardConstraint{{Kind: "exclude_artist", Value: tc.constraint}}
			}
			got := intent.Normalized()
			found := false
			for _, artist := range got.Constraints.ArtistsExclude {
				found = found || artist == "Canonical Artist"
			}
			if found != tc.wantCanonical {
				t.Fatalf("canonical exclusion=%v want=%v: %+v", found, tc.wantCanonical, got.Constraints)
			}
		})
	}
}
