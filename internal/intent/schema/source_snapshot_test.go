package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestSuppliedSourceSnapshotIsImmutableAcrossAttempts(t *testing.T) {
	prompt := "12 songs like Nine Inch Nails; include Hurt by Nine Inch Nails exactly once."
	source := lexicon.Extract(prompt)
	source.Version = "frozen-test-source/v1"
	source.Proposals = []core.IntentProposal{{Kind: "entity", Value: "Nine Inch Nails", Advisory: true}}
	before := copySource(source)
	raw, _ := json.Marshal(Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 30,
		EssentialCriteria: []WireCriterion{{Kind: "genre", Value: "invented", Scope: "playlist", Span: "invented"}}})
	for range 2 {
		m, err := ParseForPromptWithSource(raw, prompt, source)
		if err != nil {
			t.Fatal(err)
		}
		if m.Count != 12 || len(m.RequiredTracks) != 1 || m.RequiredTracks[0].Query != "Hurt by Nine Inch Nails" || m.Translation.Version != source.Version || len(m.Translation.Proposals) != 1 {
			t.Fatalf("frozen source replaced: %+v", m)
		}
		if !reflect.DeepEqual(source, before) {
			t.Fatal("compilation mutated the supplied source")
		}
		m.Translation.Atoms[0].Evidence[0].Text = "mutated returned result"
		m.Translation.Proposals[0].Value = "mutated returned proposal"
		if !reflect.DeepEqual(source, before) {
			t.Fatal("compiled intent aliases the source snapshot")
		}
	}
}

func TestSourceSnapshotRejectsDifferentPrompt(t *testing.T) {
	source := lexicon.Extract("12 songs like Nine Inch Nails")
	raw, _ := json.Marshal(Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 12})
	if _, err := ParseForPromptWithSource(raw, "12 songs like Another Artist", source); err == nil {
		t.Fatal("stale source accepted for a different request")
	}
}

func TestWirePayloadPreservesStartDurationAndScopedPreferences(t *testing.T) {
	const prompt = "One hour and fifteen minutes of music. Begin with Nine Inch Nails and finish with Marilyn Manson. Keep the opening section instrumental."
	const raw = `{"genres":[],"temporal":[],"destination":[{"kind":"artist","value":"Marilyn Manson","influence":"positive","explicit":true,"span":"Marilyn Manson"}],"genre_expansions":[],"references":[],"inferred_anchors":[],"required_tracks":[],"essential_criteria":[],"styles":[],"moods":[],"instrumentation":[],"vocal_preference":{"value":"instrumental","influence":"positive","explicit":true,"span":"opening section instrumental","scope":"journey_start","strength":"required"},"textures":[],"hard_constraints":[],"unsupported_requirements":[],"mode":"journey","journey_waypoints":[],"energy_trajectory":[],"total_count":20,"audio_weight":0.5,"cooccurrence_weight":0.5,"discovery":0.2,"artist_diversity":0.7,"transition_smoothness":0.6,"notes":"","start":[{"kind":"artist","value":"Nine Inch Nails","influence":"positive","explicit":true,"span":"Nine Inch Nails"}],"duration_seconds":4500}`
	var w Wire
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatal(err)
	}
	if w.ToCore().DurationSeconds != 4500 || w.ToCore().Start == nil {
		t.Fatal("wire-to-core lost new fields")
	}
	m, err := ParseForPrompt([]byte(raw), prompt)
	if err != nil {
		t.Fatal(err)
	}
	if m.DurationSeconds != 4500 || m.Start == nil || m.Start.Query != "Nine Inch Nails" || m.Destination == nil || m.Destination.Query != "Marilyn Manson" || core.WantsInstrumental(m) {
		t.Fatalf("consumed wire parity lost: %+v", m)
	}
	if p := m.Preferences.VocalRequests(); len(p) != 1 || p[0].Scope != "journey_start" || p[0].Strength != "required" {
		t.Fatalf("scope/strength lost: %+v", p)
	}
	saved, _ := json.Marshal(m)
	var loaded core.MusicIntent
	if err := json.Unmarshal(saved, &loaded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Normalized(), loaded.Normalized()) {
		t.Fatal("saved consumed intent changed on round trip")
	}
	for _, field := range []string{`\"start\":`, `\"duration_seconds\":`, `\"scope\":`, `\"strength\":`} {
		if !strings.Contains(GBNF, field) {
			t.Fatalf("wire field missing from grammar: %s", field)
		}
	}
}

func TestNegatedWireStartAndDurationNeverBecomePositiveRequirements(t *testing.T) {
	const prompt = "12 tracks like Nine Inch Nails. Don't start with Nine Inch Nails. Not a 12-minute playlist."
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 12, DurationSeconds: 720,
		Start: []WireReference{{Kind: "artist", Value: "Nine Inch Nails", Influence: "positive", Explicit: true, Span: "Nine Inch Nails"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if m.Start != nil || m.DurationSeconds != 0 || m.Count != 12 {
		t.Fatalf("negative controls became positive: %+v", m)
	}
}
