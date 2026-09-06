package rules

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func parse(t *testing.T, prompt string) core.MusicIntent {
	t.Helper()
	m, err := (&Parser{}).Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	if err != nil {
		t.Fatalf("Parse(%q): %v", prompt, err)
	}
	return m
}

func TestSeeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prompt string
		seeds  []string
		mode   core.Mode
	}{
		{"upbeat instrumental tracks like Justice, 20 songs", []string{"Justice"}, core.ModeSimilar},
		{"something similar to Boards of Canada", []string{"Boards of Canada"}, core.ModeSimilar},
		{"stuff like Daft Punk and Justice", []string{"Daft Punk", "Justice"}, core.ModeSimilar},
		{"a journey from soul to techno via drum and bass", []string{"soul", "drum and bass", "techno"}, core.ModeJourney},
		{"from Nick Drake to Aphex Twin", []string{"Nick Drake", "Aphex Twin"}, core.ModeJourney},
		{"Bonobo vibes", []string{"Bonobo"}, core.ModeSimilar},
		{"play Radiohead", []string{"Radiohead"}, core.ModeSimilar},
		{"make me a playlist", nil, core.ModeSimilar},
	}
	for _, tc := range cases {
		m := parse(t, tc.prompt)
		if !reflect.DeepEqual(m.Seeds.Queries, tc.seeds) {
			t.Errorf("%q: seeds = %#v, want %#v", tc.prompt, m.Seeds.Queries, tc.seeds)
		}
		if m.Mode != tc.mode {
			t.Errorf("%q: mode = %q, want %q", tc.prompt, m.Mode, tc.mode)
		}
	}
}

func TestSeedFromNowPlaying(t *testing.T) {
	t.Parallel()
	m, _ := (&Parser{}).Parse(context.Background(), ports.IntentInput{
		Prompt:     "keep it going but weirder",
		NowPlaying: &core.TrackRef{Artist: "Kavinsky", Title: "Nightcall"},
	})
	if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Kavinsky Nightcall" {
		t.Fatalf("seeds = %#v", m.Seeds.Queries)
	}
	if m.Creativity <= core.DefaultCreativity {
		t.Fatalf("'weirder' should raise creativity, got %v", m.Creativity)
	}
}

func TestCount(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"like Air, 12 tracks":            12,
		"like Air with 40 songs":         40,
		"give me a dozen songs like Air": 12,
		"twenty songs like Portishead":   20,
		"like Portishead":                core.DefaultCount,
		"like Portishead, 999 tracks":    core.MaxCount, // clamped
	}
	for prompt, want := range cases {
		if got := parse(t, prompt).Count; got != want {
			t.Errorf("%q: count = %d, want %d", prompt, got, want)
		}
	}
}

func TestCreativityDirection(t *testing.T) {
	t.Parallel()
	adventurous := parse(t, "like Bonobo but adventurous, deep cuts, surprise me")
	familiar := parse(t, "like Bonobo, keep it safe and familiar, the hits")
	if !(adventurous.Creativity > 0.66) {
		t.Errorf("adventurous creativity = %v", adventurous.Creativity)
	}
	if !(familiar.Creativity < 0.34) {
		t.Errorf("familiar creativity = %v", familiar.Creativity)
	}
}

func TestNoiseAndLookback(t *testing.T) {
	t.Parallel()
	wander := parse(t, "like Four Tet, let it wander and drift, unpredictable")
	tight := parse(t, "like Four Tet, keep it smooth cohesive and focused")
	if !(wander.Noise > 0.4) {
		t.Errorf("wander noise = %v", wander.Noise)
	}
	if !(tight.Noise < 0.1) {
		t.Errorf("tight noise = %v", tight.Noise)
	}

	if parse(t, "like Air, one track at a time").Lookback != 1 {
		t.Error("short-memory lookback")
	}
	if parse(t, "like Air, stay on theme").Lookback != 5 {
		t.Error("long-memory lookback")
	}
}

func TestConstraints(t *testing.T) {
	t.Parallel()
	m := parse(t, "like Justice but nothing by Skrillex, and no more Deadmau5")
	got := m.Constraints.ArtistsExclude
	if len(got) != 2 || !contains(got, "Skrillex") || !contains(got, "Deadmau5") {
		t.Fatalf("excludes = %#v", got)
	}
	if !m.Constraints.NoRepeatArtistBackToBack {
		t.Error("back-to-back rule should default on")
	}
	if parse(t, "like Justice, same artist is ok").Constraints.NoRepeatArtistBackToBack {
		t.Error("'same artist is ok' should turn the rule off")
	}
}

func TestTypedReferencesNegationAndRequiredTrack(t *testing.T) {
	t.Parallel()
	m := parse(t, "tracks like Justice and Daft Punk, but not Skrillex; must include Air - La femme d'argent")
	var positive, negative int
	for _, ref := range m.References {
		switch ref.Influence {
		case core.InfluencePositive:
			positive++
		case core.InfluenceNegative:
			negative++
		}
	}
	if positive != 2 || negative != 1 {
		t.Fatalf("typed references = %+v", m.References)
	}
	if len(m.RequiredTracks) != 1 || m.RequiredTracks[0].Query != "Air - La femme d'argent" {
		t.Fatalf("required tracks = %+v", m.RequiredTracks)
	}
	if len(m.RequiredTracks[0].Evidence) == 0 || !m.RequiredTracks[0].Evidence[0].Explicit {
		t.Fatalf("required evidence missing: %+v", m.RequiredTracks[0])
	}
}

func TestPreservesUnsupportedTexturePrompt(t *testing.T) {
	t.Parallel()
	prompt := "ambient electronic with microdetail, a deep groove, occasional sparkle, relaxing but not sleepy, no abstract drone"
	m := parse(t, prompt)
	for _, phrase := range []string{"microdetail", "a deep groove", "occasional sparkle", "relaxing but not sleepy"} {
		found := false
		for _, preference := range m.Preferences.TextureDescriptions {
			if preference.Value == phrase {
				found = true
			}
		}
		if !found {
			t.Errorf("texture %q not preserved: %+v", phrase, m.Preferences.TextureDescriptions)
		}
	}
	if len(m.Unsupported) != 1 || m.Unsupported[0].Text != "no abstract drone" {
		t.Fatalf("unsupported strict requirement = %+v", m.Unsupported)
	}
	for _, constraint := range m.HardConstraints {
		if constraint.Kind == "exclude_style" && constraint.Supported {
			t.Fatal("unsupported style exclusion was presented as enforced")
		}
	}
}

func TestNotesAndInfo(t *testing.T) {
	t.Parallel()
	p := &Parser{}
	if got := p.Info(); got.Backend != "rules" || !got.Ready {
		t.Fatalf("Info = %+v", got)
	}
	m := parse(t, "like Justice, 20 songs, adventurous")
	if !strings.Contains(m.NotesForUser, "Justice") || !strings.Contains(m.NotesForUser, "20 tracks") {
		t.Fatalf("notes = %q", m.NotesForUser)
	}
	// intent must survive Normalized() ranges
	if m.Count < core.MinCount || m.Count > core.MaxCount || m.Creativity < 0 || m.Creativity > 1 {
		t.Fatalf("out of range: %+v", m)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
