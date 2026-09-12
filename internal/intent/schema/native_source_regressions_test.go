package schema

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

// These are the actual raw completions (including a correction attempt) from
// the first native 3B run after adding the compact intent pipeline. Keep their
// errors intact; the compiler must recover only independently grounded facts.
func TestNativeSourceRegressionPayloads(t *testing.T) {
	raw, err := os.ReadFile("testdata/native-source-regressions-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Prompt    string
		Responses []json.RawMessage
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 6 {
		t.Fatalf("unexpected native fixture count: %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Prompt, func(t *testing.T) {
			for _, response := range tc.Responses {
				m, err := ParseForPrompt(response, tc.Prompt)
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case strings.Contains(tc.Prompt, "Aphex Twin"):
					assertNativeReference(t, m, "Aphex Twin", core.InfluencePositive)
					assertNativeReference(t, m, "Earth, Wind & Fire", core.InfluencePositive)
					if m.Start == nil || m.Start.Query != "Aphex Twin" || m.Destination == nil || m.Destination.Query != "Earth, Wind & Fire" {
						t.Fatalf("actual endpoints changed: %+v", m)
					}
				case strings.Contains(tc.Prompt, "feel of Aerosmith"):
					assertNativeReference(t, m, "Aerosmith", core.InfluencePositive)
					assertNativeConstraint(t, m, "exclude_artist", "Aerosmith")
					if len(m.RequiredTracks) != 0 || len(m.EssentialCriteria) != 0 {
						t.Fatal("similarity or soft preferences became required")
					}
				case strings.Contains(tc.Prompt, "Christian Löffler"):
					assertNativeReference(t, m, "Christian Löffler", core.InfluencePositive)
					if len(m.RequiredTracks) != 0 || len(m.EssentialCriteria) != 0 {
						t.Fatal("soft electronic request became essential")
					}
					foundElectronic := false
					for _, p := range m.Preferences.Genres {
						foundElectronic = foundElectronic || p.Value == "electronic"
						if p.Value == "electronic" && (p.Strength != "preferred" || p.Influence != core.InfluencePositive) {
							t.Fatalf("electronic meaning changed: %+v", p)
						}
					}
					if !foundElectronic {
						t.Fatal("soft electronic preference was lost")
					}
				case strings.Contains(tc.Prompt, "either band"):
					for _, artist := range []string{"Alice in Chains", "Stone Temple Pilots"} {
						assertNativeReference(t, m, artist, core.InfluencePositive)
						assertNativeConstraint(t, m, "exclude_artist", artist)
					}
				case strings.Contains(tc.Prompt, "neither rock"):
					// The established compiler/consumer contract uses exclude_style
					// for these category exclusions. Neither is lost or weakened.
					assertNativeConstraint(t, m, "exclude_style", "rock")
					assertNativeConstraint(t, m, "exclude_style", "metal")
					if len(m.References) != 0 || len(m.RequiredTracks) != 0 || m.Destination != nil || len(m.Temporal) != 0 || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "soul" {
						t.Fatalf("invented entities/criteria retained: %+v", m)
					}
				case strings.Contains(tc.Prompt, "opening section"):
					if m.Mode != core.ModeJourney || core.WantsInstrumental(m) {
						t.Fatal("journey or scoped vocals flattened")
					}
					for value, scope := range map[string]string{"electronic": "journey_start", "industrial rock": "journey_end", "instrumental": "journey_start"} {
						found := false
						for _, c := range m.EssentialCriteria {
							found = found || c.Value == value && c.Scope == scope
						}
						if !found {
							t.Fatalf("missing %s at %s: %+v", value, scope, m.EssentialCriteria)
						}
					}
				}
			}
		})
	}
}

func assertNativeReference(t *testing.T, m core.MusicIntent, artist string, influence core.Influence) {
	t.Helper()
	for _, r := range m.References {
		if r.Kind == core.ReferenceArtist && r.Query == artist && r.Influence == influence {
			return
		}
	}
	t.Fatalf("missing %s artist %q: %+v", influence, artist, m.References)
}

func assertNativeConstraint(t *testing.T, m core.MusicIntent, kind, value string) {
	t.Helper()
	for _, c := range m.HardConstraints {
		if c.Kind == kind && c.Value == value {
			return
		}
	}
	t.Fatalf("missing %s %q: %+v", kind, value, m.HardConstraints)
}
