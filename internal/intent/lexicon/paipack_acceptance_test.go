package lexicon_test

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestPaipackPriorityPrompts(t *testing.T) {
	for _, prompt := range []string{"ambient with lots of piano", "radiohead going to marilyn manson", "classical music with chello", "lively dance music from the 1990s"} {
		t.Run(prompt, func(t *testing.T) {
			m, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if err != nil {
				t.Fatal(err)
			}
			switch prompt {
			case "ambient with lots of piano":
				found := false
				ambient := false
				for _, p := range m.Preferences.Genres {
					ambient = ambient || p.Value == "ambient"
				}
				for _, p := range m.Preferences.Instrumentation {
					found = found || p.Value == "piano" && p.Degree == "mostly" && p.Strength == "essential"
				}
				if !found || !ambient {
					t.Fatalf("piano prominence lost: %+v", m)
				}
			case "radiohead going to marilyn manson":
				assertJourney("radiohead", "marilyn manson")(t, m)
			case "classical music with chello":
				found := false
				classical := false
				for _, p := range m.Preferences.Genres {
					classical = classical || p.Value == "classical"
				}
				for _, p := range m.Preferences.Instrumentation {
					found = found || p.Value == "cello"
				}
				if !found || !classical || len(m.Preferences.VocalPreferences) > 0 {
					t.Fatalf("cello alias lost: %+v", m)
				}
			case "lively dance music from the 1990s":
				assertPeriod("original_release", 1990, 1999)(t, m)
				lively, dance := false, false
				for _, p := range m.Preferences.Moods {
					lively = lively || p.Value == "energetic"
				}
				for _, p := range m.Preferences.Genres {
					dance = dance || p.Value == "dance"
				}
				if !lively || !dance {
					t.Fatalf("lively dance semantics lost: %+v", m)
				}
			}
		})
	}
}
