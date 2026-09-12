package lexicon

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestExtractProtectsNamedEntitiesAndDescriptors(t *testing.T) {
	tests := []struct{ prompt, kind, value, polarity, strength string }{
		{"Dubstep but with no skrillex or bassnectar", "genre", "dubstep", "positive", "essential"},
		{"Dubstep but with no skrillex or bassnectar", "exclude_artist", "bassnectar", "negative", "required"},
		{"Make a 15-song workout playlist like Aerosmith: driving guitar riffs and high energy, but don't include Aerosmith themselves.", "exclude_artist", "Aerosmith", "negative", "required"},
		{"Give me 12 bluesy hard rock tracks like early Aerosmith, with a raw live-band feel rather than polished pop.", "artist", "Aerosmith", "positive", "preferred"},
		{"20 relaxing electronic tracks like christrian loeffler, with warm textures and a gentle pulse. Nothing too clubby.", "artist", "christrian loeffler", "positive", "preferred"},
		{"Something like Christian Löffler and Kiasmos for a rainy evening: melancholic but comforting, mostly instrumental. 15 tracks.", "vocal", "instrumental", "positive", "preferred"},
		{"Build a 30-minute running playlist that starts easy, picks up the pace, then cools down. I like electronic music, but no harsh vocals.", "duration", "1800", "positive", "required"},
		{"Build a 30-minute running playlist that starts easy, picks up the pace, then cools down. I like electronic music, but no harsh vocals.", "vocal", "harsh vocals", "negative", "required"},
		{"15 dark industrial rock tracks like Nine Inch Nails, but less aggressive, no Marilyn Manson, and no screaming.", "mood", "aggressive", "negative", "preferred"},
		{"classical music from the 20th century with lots of dynamics transitioning to miles davis by the end of the playlist", "destination", "miles davis", "positive", "required"},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.value, func(t *testing.T) {
			x := Extract(tt.prompt)
			found := false
			for _, a := range x.Atoms {
				if a.Kind == tt.kind && a.Value == tt.value && a.Polarity == tt.polarity && a.Strength == tt.strength {
					found = true
				}
				for _, e := range a.Evidence {
					if e.Start < 0 || e.End > len(tt.prompt) || tt.prompt[e.Start:e.End] != e.Text {
						t.Fatalf("ungrounded atom: %+v", a)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s %s %s %s: %+v", tt.kind, tt.value, tt.polarity, tt.strength, x.Atoms)
			}
		})
	}
}

func TestNegativeAlternativesAreSeparateExclusions(t *testing.T) {
	x := Extract("no rock or metal")
	var negatives int
	for _, a := range x.Atoms {
		if a.Kind == "genre" && a.Polarity == "negative" {
			negatives++
			if a.Group != "" {
				t.Fatalf("negated alternatives became positive OR group: %+v", a)
			}
		}
	}
	if negatives != 2 {
		t.Fatalf("missing exclusion union: %+v", x.Atoms)
	}
}

func TestStageVocalPreferencesNeverBecomeGlobalConstraints(t *testing.T) {
	prompt := "start with no vocals and end with no screaming"
	m := Reconcile(core.MusicIntent{}, Extract(prompt))
	if len(m.Preferences.VocalPreferences) != 2 || m.Preferences.VocalPreferences[0].Scope != "journey_start" || m.Preferences.VocalPreferences[1].Scope != "journey_end" || m.Preferences.VocalPreferences[1].Influence != core.InfluenceNegative {
		t.Fatalf("typed stages lost: %+v", m.Preferences)
	}
	if core.WantsInstrumental(m) || len(m.HardConstraints) != 0 || len(m.Preferences.TextureDescriptions) != 0 {
		t.Fatalf("stage vocals were flattened or changed kind: %+v", m)
	}
}

func TestReconcileRemovesMuddledKindsAndInventedPeriods(t *testing.T) {
	prompt := "15 melancholic classical tracks, mostly piano and strings, no singing"
	x := Extract(prompt)
	e := func(s string) []core.SourceEvidence {
		return []core.SourceEvidence{{Text: s, Start: -1, End: -1, Explicit: true}}
	}
	m := core.MusicIntent{Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "melancholic", Evidence: e("melancholic")}}}, EssentialCriteria: []core.MusicalCriterion{{Kind: "texture", Value: "piano", Evidence: e("piano")}}, Temporal: []core.TemporalRequirement{{Basis: "composition", StartYear: 1990, EndYear: 2023}}}
	m = Reconcile(m, x)
	if len(m.Temporal) != 0 {
		t.Fatal("invented period retained")
	}
	for _, c := range m.EssentialCriteria {
		if c.Value == "melancholic" || c.Value == "piano" || c.Value == "strings" {
			t.Fatalf("soft description remains essential: %+v", c)
		}
	}
	if m.Preferences.VocalPreference == nil || m.Preferences.VocalPreference.Value != "instrumental" {
		t.Fatal("no singing lost")
	}
}

func TestExtractDoesNotInventArtistsOrFlattenScopes(t *testing.T) {
	for _, prompt := range []string{"from ambient to energetic electronic", "no harsh vocals", "not only piano", "classical or ambient", "music by Electronic", "play Aesop Rock"} {
		x := Extract(prompt)
		if prompt == "music by Electronic" || prompt == "play Aesop Rock" {
			for _, a := range x.Atoms {
				if a.Kind == "genre" {
					t.Fatalf("name became genre for %q: %+v", prompt, x.Atoms)
				}
			}
			continue
		}
		for _, a := range x.Atoms {
			if entityKind(a.Kind) {
				t.Fatalf("description became entity for %q: %+v", prompt, x.Atoms)
			}
		}
		if prompt == "classical or ambient" {
			if len(x.Atoms) != 2 || x.Atoms[0].Group == "" || x.Atoms[1].Group != x.Atoms[0].Group {
				t.Fatalf("alternative group lost: %+v", x.Atoms)
			}
		}
	}
}

func TestArtistJourneyHasActualEndpoints(t *testing.T) {
	prompt := "Take me from Nine Inch Nails to Marilyn Manson over 15 tracks, with smooth transitions and no repeat artists back to back."
	m := Reconcile(core.MusicIntent{}, Extract(prompt))
	if m.Start == nil || m.Start.Query != "Nine Inch Nails" || m.Destination == nil || m.Destination.Query != "Marilyn Manson" || m.Controls.TotalTrackCount != 15 {
		t.Fatalf("endpoints lost: %+v", m)
	}
}
