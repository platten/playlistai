package lexicon_test

import (
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestDefiningDescriptionsKeepAdjectivesAndLogicalRoles(t *testing.T) {
	for _, tc := range []struct {
		prompt, text, scope string
		essential           bool
	}{
		{"soft piano with spacious reverberation", "soft piano", "playlist", true},
		{"soft piano with spacious reverberation", "spacious reverberation", "playlist", true},
		{"pounding drums and distorted guitars", "distorted guitars", "playlist", true},
		{"optionally soft piano", "soft piano", "playlist", false},
		{"warm intimate vocals", "warm intimate vocals", "playlist", true},
		{"dark tense atmosphere", "dark tense atmosphere", "playlist", true},
		{"sweeping orchestral arrangements", "sweeping orchestral arrangements", "playlist", true},
		{"shimmering guitars", "shimmering guitars", "playlist", true},
		{"soft piano, optionally pounding drums and shimmering guitars", "shimmering guitars", "playlist", false},
		{"from soft piano to pounding drums", "pounding drums", "journey_end", true},
	} {
		t.Run(tc.prompt+tc.text, func(t *testing.T) {
			intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, lexicon.Extract(tc.prompt))
			found := false
			for _, c := range audio.Clauses(intent) {
				if c.Text == tc.text {
					found = true
					if c.Essential != tc.essential || c.Scope != tc.scope {
						t.Fatalf("source role changed: %+v", c)
					}
				}
			}
			if !found {
				t.Fatalf("lost literal description: %+v", intent)
			}
		})
	}
	prompt := "soft piano or pounding drums"
	intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, lexicon.Extract(prompt))
	clauses := audio.Clauses(intent)
	if len(clauses) != 2 || clauses[0].Group == "" || clauses[0].Group != clauses[1].Group {
		t.Fatalf("OR alternatives flattened: %+v", clauses)
	}
}

func TestDetailedTextureAndDeepGrooveSourceRoles(t *testing.T) {
	for _, tc := range []struct{ prompt, value, polarity, strength, scope string }{

		{"Start soft piano and add a subtle pulse in the middle", "subtle pulse", "positive", "essential", "journey_via"},
		{"Start soft piano and add a subtle pulse in the middle", "soft piano", "positive", "essential", "journey_start"},
		{"warm acoustic instruments", "warm acoustic instruments", "positive", "essential", "playlist"},
		{"gentle rhythms", "gentle rhythms", "positive", "essential", "playlist"},
		{"an intimate late-night feel", "intimate late-night feel", "positive", "essential", "playlist"},
		{"melodic bass lines", "melodic bass lines", "positive", "essential", "playlist"},
		{"bright indie-pop energy", "bright indie-pop energy", "positive", "essential", "playlist"},
		{"dramatic contrasts and a cinematic atmosphere", "cinematic atmosphere", "positive", "essential", "playlist"},
		{"a reflective atmosphere", "reflective atmosphere", "positive", "essential", "playlist"},
		{"syncopated bass and lively percussion", "lively percussion", "positive", "essential", "playlist"},
		{"a celebratory dance groove", "celebratory dance groove", "positive", "essential", "playlist"},
		{"occasional dramatic contrasts", "dramatic contrasts", "positive", "preferred", "playlist"},
		{"optionally martelé rhythms or bright indie-pop energy", "bright indie-pop energy", "positive", "preferred", "playlist"},
		{"no martelé rhythms or intimate late-night feel", "intimate late-night feel", "negative", "required", "playlist"},
		{"Start sparse, build a subtle pulse in the middle, and finish calmly.", "sparse", "positive", "essential", "journey_start"},
		{"Start sparse, build a subtle pulse in the middle, and finish calmly.", "subtle pulse", "positive", "essential", "journey_via"},
		{"Start sparse, build a subtle pulse in the middle, and finish calmly.", "calmly", "positive", "essential", "journey_end"},
		{"from traditional soul through funk to disco", "traditional soul", "positive", "essential", "journey_start"},
		{"with detailed textures and a deep groove", "detailed textures", "positive", "essential", "playlist"},
		{"with detailed textures and a deep groove", "deep groove", "positive", "essential", "playlist"},
		{"optionally detailed textures", "detailed textures", "positive", "preferred", "playlist"},
		{"deep groove if possible", "deep groove", "positive", "preferred", "playlist"},
		{"no detailed textures or deep groove", "deep groove", "negative", "required", "playlist"},
		{"without deep groove", "deep groove", "negative", "required", "playlist"},
		{"preferably no detailed textures", "detailed textures", "negative", "preferred", "playlist"},
		{"from detailed textures to deep groove", "deep groove", "positive", "essential", "journey_end"},
	} {
		t.Run(tc.prompt+tc.value, func(t *testing.T) {
			source := lexicon.Extract(tc.prompt)
			found := false
			for _, atom := range source.Atoms {
				if atom.Value != tc.value {
					continue
				}
				found = true
				if atom.Polarity != tc.polarity || atom.Strength != tc.strength || atom.Scope != tc.scope || atom.ConceptID != "" {
					t.Fatalf("wrong literal source role: %+v", atom)
				}
			}
			if !found {
				t.Fatalf("literal phrase missing: %+v", source)
			}
			intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, source)
			retained := false
			for _, clause := range audio.Clauses(intent) {
				retained = retained || clause.Text == tc.value
			}
			if !retained {
				t.Fatalf("literal phrase missing after reconcile: %+v", intent)
			}
		})
	}
	source := lexicon.Extract("detailed textures or deep groove")
	if len(source.Atoms) != 2 || source.Atoms[0].Group == "" || source.Atoms[0].Group != source.Atoms[1].Group {
		t.Fatalf("lost OR group: %+v", source.Atoms)
	}
}

func TestLiteralMusicalNounsDoNotInventProseCriteria(t *testing.T) {
	for _, prompt := range []string{"I feel like jazz", "make me feel", "please give me instruments", "give me the energy", "with rhythms", "with no textures"} {
		for _, atom := range lexicon.Extract(prompt).Atoms {
			if atom.Kind == "texture" || atom.Kind == "instrumentation" {
				t.Fatalf("unqualified prose became description for %q: %+v", prompt, atom)
			}
		}
	}
}

func TestLiteralDescriptionPreservesExactKnownConcept(t *testing.T) {
	source := lexicon.Extract("I love high energy")
	if len(source.Atoms) != 1 || source.Atoms[0].Value != "energetic" || source.Atoms[0].Kind != "mood" || source.Atoms[0].ConceptID != "mood.energetic" {
		t.Fatalf("known full phrase broadened: %+v", source.Atoms)
	}
}
