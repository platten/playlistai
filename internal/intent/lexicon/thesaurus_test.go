package lexicon

import "testing"

func TestThesaurusPreservesSourceAndExclusions(t *testing.T) {
	for _, tt := range []struct{ prompt, kind, value, polarity string }{
		{"joyous music", "mood", "happy", "positive"},
		{"no soothing music", "mood", "relaxing", "negative"},
		{"liquid dnb with sax", "genre", "liquid drum and bass", "positive"},
		{"liquid dnb with sax", "instrumentation", "saxophone", "positive"},
		{"dark timbre", "texture", "dark", "positive"},
		{"music with lots of shimmering spectral detail", "texture", "shimmering spectral detail", "positive"},
		{"music with plenty of reverberant spatial detail", "texture", "reverberant spatial detail", "positive"},
		{"R&B with no whispered singing", "vocal", "whispered vocals", "negative"},
	} {
		x := Extract(tt.prompt)
		found := false
		for _, atom := range x.Atoms {
			if atom.Kind == tt.kind && atom.Value == tt.value && atom.Polarity == tt.polarity {
				found = true
			}
			for _, e := range atom.Evidence {
				if e.Start < 0 || e.End > len(tt.prompt) || tt.prompt[e.Start:e.End] != e.Text {
					t.Fatalf("lost original span: %+v", atom)
				}
			}
		}
		if !found {
			t.Fatalf("%+v: %+v", tt, x.Atoms)
		}
	}
	for _, atom := range Extract(`music like "Joyful Noise"`).Atoms {
		if atom.Kind == "mood" && atom.Value == "happy" {
			t.Fatal("quoted title became a mood")
		}
	}
}
