package core

import "testing"

func TestMusicIntentNormalized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    MusicIntent
		check func(*testing.T, MusicIntent)
	}{
		{
			name: "empty gets defaults",
			in:   MusicIntent{},
			check: func(t *testing.T, m MusicIntent) {
				if m.Version != CurrentIntentVersion || m.Count != DefaultCount || m.Lookback != DefaultLookback {
					t.Fatalf("defaults not applied: %+v", m)
				}
				if m.Mode != ModeSimilar {
					t.Fatalf("want similar, got %q", m.Mode)
				}
			},
		},
		{
			name: "out of range clamps",
			in:   MusicIntent{Count: 9999, Lookback: 50, Creativity: 3, Noise: -1},
			check: func(t *testing.T, m MusicIntent) {
				if m.Count != MaxCount || m.Lookback != MaxLookback {
					t.Fatalf("ints not clamped: %+v", m)
				}
				if m.Creativity != 1 || m.Noise != 0 {
					t.Fatalf("floats not clamped: %+v", m)
				}
			},
		},
		{
			name: "two seeds implies journey",
			in:   MusicIntent{Seeds: IntentSeeds{Queries: []string{"a", "b"}}},
			check: func(t *testing.T, m MusicIntent) {
				if m.Mode != ModeJourney {
					t.Fatalf("want journey, got %q", m.Mode)
				}
			},
		},
		{
			name: "explicit mode preserved",
			in:   MusicIntent{Mode: ModeSimilar, Seeds: IntentSeeds{Queries: []string{"a", "b", "c"}}},
			check: func(t *testing.T, m MusicIntent) {
				if m.Mode != ModeSimilar {
					t.Fatalf("explicit mode overridden: %q", m.Mode)
				}
			},
		},
		{
			name: "blank seed strings dropped",
			in:   MusicIntent{Seeds: IntentSeeds{Queries: []string{"", "x", ""}}},
			check: func(t *testing.T, m MusicIntent) {
				if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "x" {
					t.Fatalf("blanks not dropped: %+v", m.Seeds.Queries)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, tc.in.Normalized())
		})
	}
}

func TestMusicIntentLegacySeedsBecomeRequired(t *testing.T) {
	t.Parallel()
	m := (MusicIntent{Version: 1, Seeds: IntentSeeds{TrackIDs: []string{"old"}}}).Normalized()
	if m.Version != CurrentIntentVersion || len(m.Required.TrackIDs) != 1 || m.Required.TrackIDs[0] != "old" {
		t.Fatalf("legacy intent not migrated: %+v", m)
	}

	v2 := (MusicIntent{Version: CurrentIntentVersion, Seeds: IntentSeeds{TrackIDs: []string{"reference"}}}).Normalized()
	if len(v2.Required.TrackIDs) != 0 {
		t.Fatalf("v2 reference was made required: %+v", v2)
	}
}

func TestIntentV2MigrationPreservesControlsAndConstraints(t *testing.T) {
	t.Parallel()
	m := (MusicIntent{
		Version:  2,
		Seeds:    IntentSeeds{TrackIDs: []string{"reference"}},
		Required: IntentSeeds{TrackIDs: []string{"required"}},
		Count:    17, Creativity: 0.8, Noise: 0.3, Lookback: 7,
		Constraints: IntentConstraints{ArtistsExclude: []string{"Blocked"}, NoRepeatArtistBackToBack: true},
	}).Normalized()
	if m.Version != CurrentIntentVersion || m.Controls.TotalTrackCount != 17 || m.Controls.AudioWeight != 0.8 || m.Controls.Discovery != 0.3 {
		t.Fatalf("controls not migrated: %+v", m)
	}
	if len(m.References) != 1 || len(m.RequiredTracks) != 1 || len(m.HardConstraints) != 2 {
		t.Fatalf("meaning not migrated: %+v", m)
	}
}

func TestIntentV3AddsResolutionContractWithoutReinterpreting(t *testing.T) {
	t.Parallel()
	in := MusicIntent{
		Version:         3,
		References:      []IntentReference{{Kind: ReferenceArtist, Query: "Björk", Influence: InfluencePositive}},
		Controls:        IntentControls{TotalTrackCount: 12, AudioWeight: .8, CooccurrenceWeight: .2},
		HardConstraints: []HardConstraint{{Kind: "exclude_artist", Value: "Other", Supported: true}},
	}
	got := in.Normalized()
	if got.Version != CurrentIntentVersion || len(got.References) != 1 || got.References[0].Query != "Björk" {
		t.Fatalf("v3 reference changed during v4 load: %+v", got)
	}
	if got.Controls.TotalTrackCount != 12 || len(got.HardConstraints) != 1 {
		t.Fatalf("v3 controls or constraints changed: %+v", got)
	}
}

func TestIntentSemanticValidation(t *testing.T) {
	t.Parallel()
	invalid := MusicIntent{
		Version: CurrentIntentVersion,
		RequiredTracks: []IntentReference{{
			Kind: ReferenceArtist, Query: "not a track", Influence: InfluenceNegative,
		}},
		Controls: IntentControls{TotalTrackCount: 10, AudioWeight: 0.5, CooccurrenceWeight: 0.5},
	}
	if err := invalid.Validate(); err == nil {
		t.Fatal("negative artist requirement passed semantic validation")
	}
	falseClaim := MusicIntent{
		Version:         CurrentIntentVersion,
		HardConstraints: []HardConstraint{{Kind: "exclude_style", Value: "drone", Supported: true}},
		Controls:        IntentControls{TotalTrackCount: 10, AudioWeight: 0.5, CooccurrenceWeight: 0.5},
	}
	if err := falseClaim.Validate(); err == nil {
		t.Fatal("unsupported hard constraint claimed enforcement")
	}
}

func TestParseDisplay(t *testing.T) {
	t.Parallel()

	got := ParseDisplay("id1", "Justice - D.A.N.C.E. - Radio Edit")
	if got.Artist != "Justice" || got.Title != "D.A.N.C.E. - Radio Edit" {
		t.Fatalf("split on first separator only: %+v", got)
	}

	bare := ParseDisplay("id2", "Untitled")
	if bare.Artist != "" || bare.Title != "Untitled" {
		t.Fatalf("bare title: %+v", bare)
	}
}

func TestTrackRefLinks(t *testing.T) {
	t.Parallel()

	ref := TrackRef{ID: "abc123", Artist: "A", Title: "B"}
	if ref.SpotifyURI() != "spotify:track:abc123" {
		t.Fatalf("uri: %q", ref.SpotifyURI())
	}
	if ref.SpotifyURL() != "https://open.spotify.com/track/abc123" {
		t.Fatalf("url: %q", ref.SpotifyURL())
	}
	if ref.Display() != "A - B" {
		t.Fatalf("display: %q", ref.Display())
	}
	if (TrackRef{}).SpotifyURI() != "" {
		t.Fatal("empty id should yield empty uri")
	}
}
