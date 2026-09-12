package core

import (
	"encoding/json"
	"testing"
)

func TestTranslationHistoryRoundTripAndIsolation(t *testing.T) {
	m := MusicIntent{Version: CurrentIntentVersion, OriginalDescription: "30 minutes, mostly instrumental", DurationSeconds: 1800,
		Start:       &IntentReference{Kind: ReferenceArtist, Query: "Start artist", Influence: InfluencePositive},
		Translation: &IntentTranslation{Version: "lexicon/v1", Atoms: []IntentAtom{{ID: "0", Kind: "duration", Value: "1800", Evidence: []SourceEvidence{{Text: "30 minutes", Start: 0, End: 10, Explicit: true}}}}, Repairs: []string{"Preserved duration"}},
		Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "instrumental", Influence: InfluencePositive, Strength: "preferred", Degree: "mostly"}}}.Normalized()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored MusicIntent
	if err = json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.DurationSeconds != 1800 || restored.Start.Query != "Start artist" || restored.Translation.Version != "lexicon/v1" || WantsInstrumental(restored) {
		t.Fatalf("lost contract: %+v", restored)
	}
	n := m.Normalized()
	n.Translation.Atoms[0].Evidence[0].Text = "changed"
	n.Translation.Repairs[0] = "changed"
	if m.Translation.Atoms[0].Evidence[0].Text != "30 minutes" || m.Translation.Repairs[0] != "Preserved duration" {
		t.Fatal("normalization aliases saved translation")
	}
	old := (MusicIntent{Version: 8, OriginalDescription: "30 minutes"}).Normalized()
	if old.Translation != nil || old.DurationSeconds != 0 || old.Start != nil {
		t.Fatal("historical request was reinterpreted")
	}
}

func TestPreferredCategoryAndInstrumentalRemainSoft(t *testing.T) {
	m := MusicIntent{Version: CurrentIntentVersion, Preferences: SemanticPreferences{Genres: []IntentPreference{{Value: "electronic", Influence: InfluencePositive, Strength: "preferred"}}, VocalPreference: &IntentPreference{Value: "instrumental", Influence: InfluencePositive, Strength: "preferred"}}}.Normalized()
	if len(m.EssentialCriteria) != 0 || WantsInstrumental(m) {
		t.Fatal("preferences became requirements")
	}
	m.HardConstraints = []HardConstraint{{Kind: "exclude_vocals", Value: "vocals"}}
	if !WantsInstrumental(m) {
		t.Fatal("soft preference overrode strict exclusion")
	}
}

func TestScopedAndAlternativeInstrumentationDoNotBanVocalsGlobally(t *testing.T) {
	for _, p := range []IntentPreference{
		{Value: "instrumental", Strength: "required", Scope: "journey_end"},
		{Value: "instrumental", Strength: "preferred"},
		{Value: "instrumental", Degree: "mostly"},
		{Value: "instrumental", Group: "instrumental-or-vocal"},
	} {
		for _, m := range []MusicIntent{{Preferences: SemanticPreferences{VocalPreference: &p}}, {Preferences: SemanticPreferences{Instrumentation: []IntentPreference{p}}}} {
			if WantsInstrumental(m) {
				t.Fatalf("global vocal exclusion invented from %+v", p)
			}
		}
	}
	for _, c := range []MusicalCriterion{{Kind: "vocal", Value: "instrumental", Strength: "preferred"}, {Kind: "instrumentation", Value: "instrumental", Scope: "journey_end", Strength: "required"}, {Kind: "vocal", Value: "instrumental", Group: "alternatives"}} {
		if WantsInstrumental(MusicIntent{EssentialCriteria: []MusicalCriterion{c}}) {
			t.Fatalf("global vocal exclusion invented from %+v", c)
		}
	}
}

func TestPluralVocalsOverrideLegacyViewAndCloneEvidence(t *testing.T) {
	m := MusicIntent{Version: CurrentIntentVersion, Preferences: SemanticPreferences{VocalPreference: &IntentPreference{Value: "instrumental"}, VocalPreferences: []IntentPreference{{Value: "harsh vocals", Influence: InfluenceNegative, Strength: "required", Scope: "journey_start", Evidence: []SourceEvidence{{Text: "no harsh vocals at the start"}}}, {Value: "vocals", Influence: InfluencePositive, Strength: "preferred", Scope: "journey_end"}}}}.Normalized()
	if WantsInstrumental(m) || len(m.Preferences.VocalRequests()) != 2 {
		t.Fatal("legacy singleton overrode scoped plural intent")
	}
	n := m.Normalized()
	n.Preferences.VocalPreferences[0].Evidence[0].Text = "changed"
	if m.Preferences.VocalPreferences[0].Evidence[0].Text == "changed" {
		t.Fatal("plural evidence aliases saved intent")
	}
	m.EssentialCriteria = []MusicalCriterion{{Kind: "genre", Value: "classical", Strength: "preferred"}}
	if _, ok := SinglePlaylistGenre(m); ok {
		t.Fatal("preferred criterion became mandatory genre")
	}
}
