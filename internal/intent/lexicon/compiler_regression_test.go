package lexicon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestCompilerPreservesMeasuredRequestSemantics(t *testing.T) {
	cases := []struct {
		prompt string
		check  func(*testing.T, core.MusicIntent)
	}{
		{"Make 12 songs similar to Nine Inch Nails. Include Hurt by Nine Inch Nails exactly once, but it does not have to open the playlist.", func(t *testing.T, m core.MusicIntent) {
			if len(m.RequiredTracks) != 1 || m.RequiredTracks[0].Query != "Hurt by Nine Inch Nails" || m.Start != nil || m.Count != 12 {
				t.Fatalf("required recording or role lost: %+v", m)
			}
			assertReferences("Nine Inch Nails")(t, m)
		}},
		{"Give me 15 songs by Aerosmith only. I prefer energetic guitars, but quieter songs are okay too.", func(t *testing.T, m core.MusicIntent) {
			assertReferences("Aerosmith")(t, m)
			found := false
			for _, c := range m.HardConstraints {
				found = found || c.Kind == "require_artist" && c.Value == "Aerosmith"
			}
			if !found {
				t.Fatal("artist-only restriction lost")
			}
		}},
		{"Give me 15 tracks by Aerosmith, released between 1973 and 1975 only.", func(t *testing.T, m core.MusicIntent) {
			assertReferences("Aerosmith")(t, m)
			assertPeriod("original_release", 1973, 1975)(t, m)
			found := false
			for _, c := range m.HardConstraints {
				found = found || c.Kind == "require_artist" && c.Value == "Aerosmith"
			}
			if !found {
				t.Fatal("dated artist-only restriction lost")
			}
		}},
		{"I'd like one hour and fifteen minutes of relaxing electronic music for working, mostly instrumental.", func(t *testing.T, m core.MusicIntent) {
			assertDuration(4500)(t, m)
			assertSoftInstrumental(t, m)
			if len(m.References) != 0 {
				t.Fatalf("quantity became artist: %+v", m.References)
			}
		}},
		{"I want 12 tracks, not a 12-minute playlist: relaxing electronic music for working, mostly instrumental.", func(t *testing.T, m core.MusicIntent) {
			if m.Count != 12 || m.DurationSeconds != 0 {
				t.Fatalf("negated duration became target: %+v", m)
			}
		}},
		{"I want 12 tracks, not one hour and fifteen minutes of music.", func(t *testing.T, m core.MusicIntent) {
			if m.Count != 12 || m.DurationSeconds != 0 {
				t.Fatalf("part of negated compound duration became target: %+v", m)
			}
		}},
		{"I want 12 tracks, not 20 tracks, with a warm sound.", func(t *testing.T, m core.MusicIntent) {
			if m.Count != 12 {
				t.Fatalf("negated track count became target: %d", m.Count)
			}
		}},
		{"I need 12 tracks for a quiet evening, similar to Christian Löffler. Prefer relaxing electronic sounds; vocals are welcome.", func(t *testing.T, m core.MusicIntent) {
			for _, c := range m.EssentialCriteria {
				if c.Value == "relaxing" || c.Value == "electronic" {
					t.Fatalf("soft preference became essential: %+v", c)
				}
			}
			assertPreference(t, m.Preferences.Genres, "electronic", core.InfluencePositive)
		}},
		{"Eight tracks like Radiohead. Prefer a warm sound over an aggressive one.", assertNegativeAggressive},
		{"Ten songs like Massive Attack for late-night listening. I prefer dark textures, not cheerful party music.", func(t *testing.T, m core.MusicIntent) {
			assertPreference(t, m.Preferences.Moods, "party", core.InfluenceNegative)
		}},
		{"A 14-song journey please: begin with Nine Inch Nails and finish with Marilyn Manson, using other artists in between.", func(t *testing.T, m core.MusicIntent) {
			if m.Count != 14 || m.Start == nil || m.Start.Query != "Nine Inch Nails" || m.Destination == nil || m.Destination.Query != "Marilyn Manson" {
				t.Fatalf("actual endpoints lost: %+v", m)
			}
		}},
		{"Build 16 tracks from relaxing electronic to industrial rock. Keep only the opening section instrumental; vocals are fine later.", func(t *testing.T, m core.MusicIntent) {
			if core.WantsInstrumental(m) {
				t.Fatal("opening instrumental became global")
			}
			found := false
			for _, p := range m.Preferences.VocalRequests() {
				found = found || p.Value == "instrumental" && p.Scope == "journey_start"
			}
			if !found {
				t.Fatal("opening vocal requirement lost")
			}
		}},
		{"Give me 10 tracks like the song Angel by Massive Attack. Don't force Angel into the playlist.", func(t *testing.T, m core.MusicIntent) {
			if len(m.References) != 1 || m.References[0].Kind != core.ReferenceTrack || m.References[0].Query != "Angel by Massive Attack" || len(m.RequiredTracks) != 0 {
				t.Fatalf("recording similarity role lost: %+v", m)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.prompt, func(t *testing.T) {
			for _, backend := range []string{"rules", "model"} {
				t.Run(backend, func(t *testing.T) {
					var m core.MusicIntent
					var err error
					if backend == "rules" {
						m, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: tc.prompt})
					} else {
						raw, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 20})
						m, err = schema.ParseForPrompt(raw, tc.prompt)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := m.Validate(); err != nil {
						t.Fatal(err)
					}
					tc.check(t, m)
				})
			}
		})
	}
}

func TestUnenforcedAlternativesAndRelativeEnergyRemainExplicit(t *testing.T) {
	for _, prompt := range []string{
		"Give me 12 electronic tracks released in 2001 or 2009, not the years in between.",
		"I need 12 tracks similar to Christian Löffler, but with more energy for a run.",
	} {
		w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 12}
		if strings.Contains(prompt, "2001") {
			text := "released in 2001 or 2009"
			start := strings.Index(prompt, text)
			w.Temporal = []core.TemporalRequirement{{Basis: "original_release", StartYear: 2001, EndYear: 2009, Scope: "playlist", Evidence: []core.SourceEvidence{{Text: text, Start: start, End: start + len(text), Explicit: true}}}}
		}
		raw, _ := json.Marshal(w)
		m, err := schema.ParseForPrompt(raw, prompt)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Unsupported) == 0 {
			t.Fatal("unenforced source requirement disappeared")
		}
		if strings.Contains(prompt, "2001") && len(m.Temporal) != 0 {
			t.Fatal("distinct year choices broadened to a continuous interval")
		}
	}
}

func TestOwnershipDoesNotCrossDistinctNameOccurrences(t *testing.T) {
	prompt := "Like Nine Inch Nails. Include Hurt by Nine Inch Nails exactly once."
	start := strings.Index(prompt, "Hurt")
	end := start + len("Hurt by Nine Inch Nails")
	evidence := []core.SourceEvidence{{Text: prompt[start:end], Start: start, End: end, Explicit: true}}
	x := lexicon.Extract(prompt)
	var other []core.IntentAtom
	for _, a := range x.Atoms {
		if a.Kind == "artist" {
			other = append(other, a)
		}
	}
	if lexicon.Owned("Hurt by Nine Inch Nails", evidence, other) {
		t.Fatal("separate similarity artist owns required recording occurrence")
	}
	if !lexicon.Owned("Hurt by Nine Inch Nails", evidence, x.Atoms) {
		t.Fatal("actual required occurrence did not own its source")
	}
}

func TestQualifiedTrackSyntaxDoesNotTruncateLiteralTitles(t *testing.T) {
	for _, name := range []string{"Fixture Artist - Once", "Fixture Artist - Once in a Lifetime", `Fixture Artist - Exactly Once`, `Fixture Artist - Warm and Keep Going`} {
		prompt := `12 tracks, include "` + name + `"`
		got := lexicon.RequiredTracks(prompt)
		if len(got) != 1 || got[0].Query != name {
			t.Fatalf("quoted title changed: %q -> %+v", name, got)
		}
	}
	for _, name := range []string{"Fixture Artist - Once", "Fixture Artist - Once in a Lifetime"} {
		got := lexicon.RequiredTracks("12 tracks, include " + name)
		if len(got) != 1 || got[0].Query != name {
			t.Fatalf("title changed: %q -> %+v", name, got)
		}
	}
}

func TestLayeredSoundOverDoesNotInventNegativePreference(t *testing.T) {
	prompt := "Warm synth over aggressive drums."
	for _, a := range lexicon.Extract(prompt).Atoms {
		if a.Value == "aggressive" && a.Polarity != "positive" {
			t.Fatalf("layering became contrast: %+v", a)
		}
	}
}

func TestUnitWordsInAnArtistNameRemainAnIdentity(t *testing.T) {
	prompt := "Music like One Minute Silence, 12 tracks."
	m := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, lexicon.Extract(prompt)).Normalized()
	assertReferences("One Minute Silence")(t, m)
	if m.DurationSeconds != 0 {
		t.Fatal("unit in artist name became a duration")
	}
}

func TestArtistPronounExclusionRequiresOneAdjacentLiteralPair(t *testing.T) {
	for _, prompt := range []string{
		"Like Alpha and Beta, without either band's recordings.",
		"Like Alpha and Beta, excluding both artists' songs.",
	} {
		m := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, lexicon.Extract(prompt)).Normalized()
		for _, artist := range []string{"Alpha", "Beta"} {
			positive, excluded := false, false
			for _, ref := range m.References {
				positive = positive || ref.Query == artist && ref.Influence == core.InfluencePositive
			}
			for _, c := range m.HardConstraints {
				excluded = excluded || c.Kind == "exclude_artist" && c.Value == artist
			}
			if !positive || !excluded {
				t.Fatalf("sound reference or output exclusion lost: %+v", m)
			}
		}
	}
	for _, prompt := range []string{
		"Like Alpha, without either band's recordings.",
		"Like Alpha and Beta and Gamma, without either band's recordings.",
		"Like Alpha and Beta. Without either band's recordings.",
		"Like Alpha and Beta, with another artist, without either band's recordings.",
		"Like Alpha and Alpha, without either band's recordings.",
		"Like Alpha and Beta, don't exclude either band's recordings.",
		"Leave slow ballads out of the results.",
	} {
		for _, atom := range lexicon.Extract(prompt).Atoms {
			if atom.Kind == "exclude_artist" {
				t.Fatalf("ambiguous/operator-free artist exclusion in %q: %+v", prompt, atom)
			}
		}
	}
}
