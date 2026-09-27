package lexicon_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
)

func TestLiteralTextureDetailsSurviveKnownSubphrase(t *testing.T) {
	const prompt = "Give me 10 songs with jangly guitars, melodic bass lines, and bright indie-pop energy."
	wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
	for _, value := range []string{"jangly guitars", "melodic bass lines"} {
		wire.Textures = append(wire.Textures, schema.WirePreference{Value: value, Span: value, Explicit: true, Influence: "positive"})
	}
	raw, _ := json.Marshal(wire)
	intent, err := schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"jangly guitars", "melodic bass lines"} {
		found := false
		for _, p := range append(intent.Preferences.TextureDescriptions, intent.Preferences.Instrumentation...) {
			if p.Value != value {
				continue
			}
			found = true
			if p.Influence != core.InfluencePositive || p.Strength != "essential" || p.Scope != "playlist" || len(p.Evidence) != 1 {
				t.Fatalf("literal preference changed role: %+v", p)
			}
			e := p.Evidence[0]
			if e.Start != strings.Index(prompt, value) || e.End != e.Start+len(value) || e.Text != value || !e.Explicit {
				t.Fatalf("literal occurrence not retained: %+v", e)
			}
		}
		if !found {
			t.Fatalf("literal detail %q lost: %+v", value, intent.Preferences)
		}
	}
}

func TestLiteralTextureExceptionCannotBypassSourceOwnership(t *testing.T) {
	for _, test := range []struct {
		prompt, value, span string
		start, end          int
	}{
		{"with melodic bass lines", "melodic brass lines", "melodic bass lines", -1, -1},
		{"with melodic bass lines", "melodic bass lines", "melodic bass lines", 0, 18},
		{"no melodic bass lines", "melodic bass lines", "melodic bass lines", -1, -1},
		{"only melodic bass lines", "melodic bass lines", "melodic bass lines", -1, -1},
		{"with mostly melodic bass lines", "melodic bass lines", "melodic bass lines", -1, -1},
		{"with melodic and bright bass lines", "melodic and bright bass lines", "melodic and bright bass lines", -1, -1},
		{`music by "Melodic Bass Lines"`, "Melodic Bass Lines", "Melodic Bass Lines", -1, -1},
	} {
		t.Run(test.prompt+"/"+test.value, func(t *testing.T) {
			source := lexicon.Extract(test.prompt)
			original := core.MusicIntent{OriginalDescription: test.prompt, Preferences: core.SemanticPreferences{TextureDescriptions: []core.IntentPreference{{Value: test.value, Explicit: true, Influence: core.InfluencePositive, Evidence: []core.SourceEvidence{{Text: test.span, Start: test.start, End: test.end, Explicit: true}}}}}}
			got := lexicon.Reconcile(original, source)
			for _, p := range got.Preferences.TextureDescriptions {
				if p.Value == test.value && p.Influence == core.InfluencePositive {
					t.Fatalf("model detail bypassed protected source meaning: %+v", p)
				}
			}
		})
	}
}
