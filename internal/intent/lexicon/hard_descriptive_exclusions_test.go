package lexicon_test

import (
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestHardDescriptionsKeepWholeNegativePhrase(t *testing.T) {
	for _, tc := range []struct {
		prompt, value string
		strict        bool
	}{
		{"no pounding drums", "pounding drums", true},
		{"without distorted guitars", "distorted guitar", true},
		{"not sleepy", "sleepy", true},
		{"neither piano nor drums", "drums", true},
		{"nothing aggressive", "aggressive", true},
		{"less pounding drums", "pounding drums", false},
		{"avoid pounding drums", "pounding drums", false},
		{"not too aggressive", "aggressive", false},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, lexicon.Extract(tc.prompt))
			found := false
			for _, c := range audio.Clauses(intent) {
				if c.Text == tc.value {
					found = true
					if !c.Negative || c.Strict != tc.strict {
						t.Fatalf("negative force lost: %+v", c)
					}
				}
				if tc.value == "pounding drums" && c.Text == "drums" || tc.value == "distorted guitar" && (c.Text == "guitar" || c.Text == "guitars") {
					t.Fatalf("qualified exclusion broadened: %+v", c)
				}
			}
			if tc.strict && !found {
				t.Fatalf("hard source exclusion lost without model: %+v", intent)
			}
		})
	}
}

func TestOptionalNegativeDescriptionsStayPreferred(t *testing.T) {
	for _, prompt := range []string{"preferably no pounding drums", "prefer no pounding drums", "if possible, without pounding drums"} {
		value := "pounding drums"
		pref := core.IntentPreference{Value: value, Explicit: true, Influence: core.InfluenceNegative, Evidence: []core.SourceEvidence{{Text: value, Start: -1, End: -1, Explicit: true}}}
		intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt, Preferences: core.SemanticPreferences{Instrumentation: []core.IntentPreference{pref}}}, lexicon.Extract(prompt))
		found := false
		for _, clause := range audio.Clauses(intent) {
			if clause.Text == value {
				found = true
				if !clause.Negative || clause.Strict || clause.Essential {
					t.Fatalf("optional negative hardened: %+v", clause)
				}
			}
		}
		if !found || len(intent.HardConstraints) != 0 {
			t.Fatalf("optional wording lost: %+v", intent)
		}
	}
}

func TestCoordinatedQualifiedNegativesRemainExclusions(t *testing.T) {
	for _, tc := range []struct {
		prompt string
		values []string
		strict bool
	}{
		{"no martelé drums or distorted guitars", []string{"martelé drums", "distorted guitars"}, true},
		{"no ringing guitar tones or pounding drums", []string{"ringing guitar tones", "pounding drums"}, true},
		{"no pounding drums or guitar tones", []string{"pounding drums", "guitar tones"}, true},
		{"no pounding drums or martelé guitar tones", []string{"pounding drums", "martelé guitar tones"}, true},
		{"no pounding drums or distorted guitars", []string{"pounding drums", "distorted guitars"}, true},
		{"without soft piano or pounding drums", []string{"soft piano", "pounding drums"}, true},
		{"no pounding drums and piano", []string{"pounding drums", "piano"}, true},
		{"no pounding drums, or soft piano", []string{"pounding drums", "soft piano"}, true},
		{"no warm intimate vocals", []string{"warm intimate vocals"}, true},
		{"preferably no pounding drums or distorted guitars", []string{"pounding drums", "distorted guitars"}, false},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, lexicon.Extract(tc.prompt))
			clauses := audio.Clauses(intent)
			if len(clauses) != len(tc.values) {
				t.Fatalf("lost or broadened phrase: %+v", clauses)
			}
			for _, value := range tc.values {
				found := false
				for _, c := range clauses {
					if c.Text == value {
						found = true
						if !c.Negative || c.Strict != tc.strict || c.Group != "" {
							t.Fatalf("negated alternatives became positive OR: %+v", c)
						}
					}
				}
				if !found {
					t.Fatalf("missing %q: %+v", value, clauses)
				}
			}
		})
	}
}

func TestPostfixOptionalDescriptionScope(t *testing.T) {
	for _, tc := range []struct {
		prompt         string
		pianoEssential bool
	}{
		{"soft piano if possible", false},
		{"soft piano, optionally pounding drums", true},
	} {
		intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, lexicon.Extract(tc.prompt))
		found := false
		for _, c := range audio.Clauses(intent) {
			if c.Text == "soft piano" {
				found = true
				if c.Essential != tc.pianoEssential {
					t.Fatalf("%s: %+v", tc.prompt, c)
				}
			}
		}
		if !found {
			t.Fatalf("lost piano phrase: %+v", intent)
		}
	}
}
