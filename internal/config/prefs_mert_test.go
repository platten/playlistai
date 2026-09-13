package config

import (
	"encoding/json"
	"testing"
)

func TestMERTPreferenceMigrationAndRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want bool
	}{
		{"new install", `{}`, false},
		{"legacy opted out", `{"enhancedAudioEnabled":false}`, false},
		{"legacy opted in", `{"enhancedAudioEnabled":true}`, true},
		{"independent MERT opt out", `{"enhancedAudioEnabled":true,"mertSimilarityEnabled":false}`, false},
		{"MERT without DSP", `{"enhancedAudioEnabled":false,"mertSimilarityEnabled":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prefs Prefs
			if err := json.Unmarshal([]byte(tc.json), &prefs); err != nil {
				t.Fatal(err)
			}
			if got := prefs.MERTSimilarityEnabledValue(); got != tc.want {
				t.Fatalf("migrated MERT enabled=%v, want %v", got, tc.want)
			}
			prefs.DebugLogging = true
			dir := t.TempDir()
			if err := prefs.Save(dir); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadPrefsChecked(dir)
			if err != nil || loaded.MERTSimilarityEnabledValue() != tc.want || !loaded.DebugLogging {
				t.Fatalf("unrelated settings save lost MERT preference: %+v %v", loaded, err)
			}
		})
	}
}
