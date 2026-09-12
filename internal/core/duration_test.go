package core

import (
	"encoding/json"
	"testing"
)

func TestDurationDefaultsAndHistoricalToleranceRoundTrip(t *testing.T) {
	for _, tc := range []struct{ duration, tolerance, want int }{{0, 0, 0}, {4500, 0, 60}, {4500, 15, 15}, {4500, 120, 120}} {
		old := MusicIntent{Version: 9, DurationSeconds: tc.duration, DurationToleranceSeconds: tc.tolerance, Controls: IntentControls{TotalTrackCount: 17}}
		normalized := old.Normalized()
		if normalized.DurationSeconds != tc.duration || normalized.DurationToleranceSeconds != tc.want || normalized.Count != 17 || !normalized.HasExplicitTrackCount() {
			t.Fatalf("legacy duration/count migration changed meaning: %+v", normalized)
		}
		raw, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		var restored MusicIntent
		if err := json.Unmarshal(raw, &restored); err != nil || restored.Normalized().DurationTolerance() != tc.want {
			t.Fatal("duration tolerance did not survive history", err)
		}
	}
}

func TestDurationCountRequiresSourceOrUserOverride(t *testing.T) {
	m := MusicIntent{DurationSeconds: 4500, Translation: &IntentTranslation{}, Controls: IntentControls{TotalTrackCount: 20}}.Normalized()
	if m.HasExplicitTrackCount() {
		t.Fatal("parser default became required count")
	}
	m.Translation.Atoms = []IntentAtom{{Kind: "count", Polarity: "negative", Strength: "required", Value: "20"}}
	if m.HasExplicitTrackCount() {
		t.Fatal("negated count became required count")
	}
	m.Translation.Atoms[0].Polarity = "positive"
	if !m.HasExplicitTrackCount() {
		t.Fatal("positive source count was lost")
	}
	m.Translation.Atoms = nil
	m.TrackCountExplicit = true
	if !m.Normalized().HasExplicitTrackCount() {
		t.Fatal("explicit control override was lost")
	}
}

func TestDurationContractRejectsInvalidValues(t *testing.T) {
	for _, m := range []MusicIntent{{DurationToleranceSeconds: 60}, {DurationSeconds: 4500, DurationToleranceSeconds: -1}, {DurationSeconds: 4500, DurationToleranceSeconds: 86401}} {
		if m.Validate() == nil {
			t.Fatalf("invalid duration contract accepted: %+v", m)
		}
	}
	for _, duration := range []*RecordingDuration{nil, {}, {Milliseconds: -1, Source: "provider", RecordingID: "id"}, {Milliseconds: 30, RecordingID: "id"}, {Milliseconds: 30, Source: "provider"}} {
		if duration.Valid() {
			t.Fatalf("incomplete duration evidence accepted: %+v", duration)
		}
	}
}

func TestSpellingDecisionPersistsWithoutChangingSourceEvidence(t *testing.T) {
	for _, decision := range []string{"", "original", "accepted"} {
		m := MusicIntent{References: []IntentReference{{Kind: ReferenceArtist, Query: "Aersomith", Influence: InfluencePositive, SpellingDecision: decision, Evidence: []SourceEvidence{{Text: "Aersomith", Start: 0, End: 9, Explicit: true}}}}}.Normalized()
		if m.Validate() != nil || m.References[0].SpellingDecision != decision || m.References[0].Evidence[0].Text != "Aersomith" {
			t.Fatal("spelling choice or literal source was lost")
		}
	}
	m := MusicIntent{References: []IntentReference{{SpellingDecision: "model-guessed"}}}
	if m.Validate() == nil {
		t.Fatal("invalid spelling decision accepted")
	}
}
