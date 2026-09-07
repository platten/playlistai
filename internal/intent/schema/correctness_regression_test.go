package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func electronicWire(t *testing.T) Wire {
	t.Helper()
	var wire Wire
	if err := json.Unmarshal([]byte(FewShot[3].JSON), &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestValidateCategoryWordingWithReferences(t *testing.T) {
	for _, prompt := range []string{"electronic tracks", "electronic songs", "electronic playlist", "electronic music like Seed Artist"} {
		t.Run(prompt, func(t *testing.T) {
			wire := electronicWire(t)
			wire.EssentialCriteria = nil
			raw, _ := json.Marshal(wire)
			if _, err := ParseForPrompt(raw, prompt); err == nil || !strings.Contains(err.Error(), "defining category") {
				t.Fatalf("dropped defining category accepted: %v", err)
			}
		})
	}
}

func TestExplicitReferenceRequiresItsNameInsideGenuineSourceSpan(t *testing.T) {
	for _, reference := range []WireReference{
		{Kind: "artist", Value: "Queen", Influence: "positive", Explicit: true, Span: "electronic music"},
		{Kind: "artist", Value: "Queen", Influence: "positive", Explicit: true, Span: "Queen"},
		{Kind: "artist", Value: "Electronic", Influence: "positive", Explicit: true, Span: "electronic"},
	} {
		t.Run(reference.Value+"/"+reference.Span, func(t *testing.T) {
			wire := electronicWire(t)
			wire.References = []WireReference{reference}
			raw, _ := json.Marshal(wire)
			for _, prompt := range []string{"electronic music", "electronic tracks"} {
				if _, err := ParseForPrompt(raw, prompt); err == nil {
					t.Fatalf("unrequested artist accepted as explicit for %q", prompt)
				}
			}
		})
	}
	for prompt, artist := range map[string]string{"play Aesop Rock": "Aesop Rock", "play music by Electronic": "Electronic", "like 坂本龍一": "坂本龍一"} {
		wire := Wire{Mode: "similar", TotalCount: 10, AudioWeight: .5, CooccurrenceWeight: .5, References: []WireReference{{Kind: "artist", Value: artist, Influence: "positive", Explicit: true, Span: artist}}}
		raw, _ := json.Marshal(wire)
		if _, err := ParseForPrompt(raw, prompt); err != nil {
			t.Errorf("legitimate artist %q rejected: %v", prompt, err)
		}
	}
}

func TestNarrowRockAndRollExclusionDoesNotRequireBroadRockExclusion(t *testing.T) {
	wire := electronicWire(t)
	wire.Styles = append(wire.Styles, WirePreference{Value: "rock & roll", Influence: "negative", Explicit: true, Span: "no rock & roll"})
	wire.HardConstraints = []WireConstraint{{Kind: "exclude_style", Value: "rock & roll", Span: "no rock & roll"}}
	raw, _ := json.Marshal(wire)
	if _, err := ParseForPrompt(raw, "electronic music, no rock & roll"); err != nil {
		t.Fatalf("correct narrow exclusion rejected: %v", err)
	}
}

func TestInferredRequiredTrackAndLegacyLiveCompletionAreRejected(t *testing.T) {
	wire := electronicWire(t)
	wire.RequiredTracks = []WireReference{{Kind: "track", Value: "Queen - One Vision", Influence: "positive", Explicit: false, Span: "electronic music"}}
	raw, _ := json.Marshal(wire)
	if _, err := ParseForPrompt(raw, "electronic music"); err == nil {
		t.Fatal("inferred required track passed validation")
	}
	legacy := []byte(`{"seeds":["Electronic"],"mode":"similar","count":10}`)
	if _, err := ParseForPrompt(legacy, "electronic music"); err == nil {
		t.Fatal("legacy model completion bypassed the current contract")
	}
	if _, err := Parse(legacy); err != nil {
		t.Fatalf("historical legacy intent no longer loads: %v", err)
	}
}

func TestEveryFewShotPreservesItsPromptMeaning(t *testing.T) {
	for _, example := range FewShot {
		if _, err := ParseForPrompt([]byte(example.JSON), example.Prompt); err != nil {
			t.Errorf("few-shot %q: %v", example.Prompt, err)
		}
	}
}
