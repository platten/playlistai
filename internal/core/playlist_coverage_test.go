package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPlaylistCoverageValidationAndLegacyRoundTrip(t *testing.T) {
	base := MusicIntent{EssentialCriteria: []MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist", CoverageGroup: "mix:10"}}}.Normalized()
	for _, test := range []struct {
		name   string
		change func(*MusicIntent)
	}{
		{"mood", func(m *MusicIntent) { m.EssentialCriteria[0].Kind = "mood" }},
		{"journey", func(m *MusicIntent) { m.EssentialCriteria[0].Scope = "journey_start" }},
		{"blank", func(m *MusicIntent) { m.EssentialCriteria[0].CoverageGroup = " " }},
		{"negative", func(m *MusicIntent) {
			m.Preferences.Genres = []IntentPreference{{Value: "house", Influence: InfluenceNegative, CoverageGroup: "mix"}}
		}},
		{"too many", func(m *MusicIntent) {
			for len(m.EssentialCriteria) <= MaxCount {
				m.EssentialCriteria = append(m.EssentialCriteria, m.EssentialCriteria[0])
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := base.Normalized()
			test.change(&m)
			if m.Validate() == nil {
				t.Fatal("invalid coverage accepted")
			}
		})
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.EssentialCriteria = append(base.EssentialCriteria, MusicalCriterion{Kind: "genre", Value: "house", Scope: "playlist", CoverageGroup: "second-set"})
	if got := base.Normalized(); len(got.EssentialCriteria) != 2 {
		t.Fatal("normalization merged independent coverage sets")
	}
	for _, marked := range []bool{false, true} {
		m := base.Normalized()
		if !marked {
			for i := range m.EssentialCriteria {
				m.EssentialCriteria[i].CoverageGroup = ""
			}
		}
		m = m.Normalized()
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if !marked && strings.Contains(string(raw), "coverageGroup") {
			t.Fatal("absent field changed legacy serialization")
		}
		var saved MusicIntent
		if err := json.Unmarshal(raw, &saved); err != nil || !reflect.DeepEqual(saved.Normalized(), m) {
			t.Fatalf("intent round trip: %v", err)
		}
		frozen, err := FreezeSearch(SearchSnapshot{PolicyVersion: EnhancedSearchPolicyVersion}, Playlist{Intent: m})
		if err != nil || frozen.Validate() != nil {
			t.Fatalf("snapshot: %v", err)
		}
		frozen.Result.Intent.EssentialCriteria[0].CoverageGroup = "tampered"
		if frozen.Validate() == nil {
			t.Fatal("coverage semantics omitted from snapshot fingerprint")
		}
	}
}
