package main

import (
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestMeaningChecksConsumedIntentRatherThanExtractionLog(t *testing.T) {
	want := &meaningExpectation{DurationSeconds: 1800, Start: "Nine Inch Nails", NoTemporal: true, SoftInstrumental: true, Preferences: []preferenceExpectation{{Kind: "texture", Value: "warm", Polarity: "positive"}}, Constraints: []constraintExpectation{{Kind: "exclude_artist", Value: "Aerosmith"}}}
	m := core.MusicIntent{Translation: &core.IntentTranslation{Atoms: []core.IntentAtom{{Kind: "duration", Value: "1800"}, {Kind: "texture", Value: "warm"}}}, Controls: core.IntentControls{TotalTrackCount: 30}}
	issues := strings.Join(checkMeaning(want, m), "\n")
	for _, expected := range []string{"meaning lost", "explicit constraint lost", "starting artist", "duration unit", "minutes silently"} {
		if !strings.Contains(issues, expected) {
			t.Fatalf("missing independent consumer check %q: %s", expected, issues)
		}
	}
	m.DurationSeconds, m.Controls.TotalTrackCount = 1800, 20
	m.Start = &core.IntentReference{Query: "Nine Inch Nails"}
	m.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "warm", Influence: core.InfluencePositive}}
	m.Preferences.VocalPreference = &core.IntentPreference{Value: "instrumental", Influence: core.InfluencePositive, Strength: "preferred"}
	m.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Aerosmith"}}
	if got := checkMeaning(want, m); len(got) != 0 {
		t.Fatalf("faithful intent rejected: %v", got)
	}
	m.Preferences.VocalPreference.Strength = "required"
	if got := checkMeaning(want, m); len(got) != 1 || !strings.Contains(got[0], "mostly instrumental") {
		t.Fatalf("strictness regression missed: %v", got)
	}
}
