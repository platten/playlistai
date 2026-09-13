package musicconcepts

import "testing"

func TestThesaurusProviderBoundaries(t *testing.T) {
	for _, tt := range []struct{ kind, word, canonical, model, label string }{
		{"mood", "joyous", "happy", "mood_happy", "happy"},
		{"mood", "soothing", "relaxing", "mood_relaxed", "relaxed"},
		{"texture", "dark timbre", "dark", "timbre", "dark"},
		{"genre", "R&B", "rhythm and blues", "genre_rosamerica", "rhy"},
		{"texture", "electronic instrumentation", "electronic production", "mood_electronic", "electronic"},
		{"mood", "dreamlike", "dreamy", "mood_relaxed", ""},
		{"genre", "liquid dnb", "liquid drum and bass", "genre_electronic", ""},
		{"texture", "dissonance", "dissonant", "tonal_atonal", ""},
		{"instrumentation", "acoustic guitars", "acoustic guitar", "mood_acoustic", ""},
	} {
		c, ok := Find(tt.kind, tt.word)
		if !ok || c.Value != tt.canonical || c.Providers.AcousticBrainz[tt.model] != tt.label {
			t.Fatalf("%+v: %+v", tt, c)
		}
	}
	for _, tt := range []struct{ model, label string }{
		{"genre_rosamerica", "rhythm and blues"}, {"mood_dreamy", "dreamy"},
		{"genre_electronic", "dnb"}, {"gender", "female"},
	} {
		if ValidAcousticClass(tt.model, tt.label) {
			t.Fatalf("unreviewed class accepted: %+v", tt)
		}
	}
	for _, c := range Concepts() {
		if len(c.Providers.CLAP) != 1 {
			t.Fatalf("missing bounded CLAP caption: %s", c.ID)
		}
	}
}
