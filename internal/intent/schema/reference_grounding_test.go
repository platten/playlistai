package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestQualifiedReferencesPreservePossessivesAndReorderedTitles(t *testing.T) {
	for _, kind := range []string{"album", "track"} {
		for _, value := range []string{"Fixture Title by Björk", "Björk - Fixture Title", "Björk’s Fixture Title"} {
			prompt := "Music like Bjork's " + kind + " Fixture Title, with a gentle mood"
			w := Wire{Genres: []WirePreference{}, References: []WireReference{{Kind: kind, Value: value, Explicit: true, Span: "Bjork's " + kind + " Fixture Title", Influence: "positive"}}, TotalCount: 6, Mode: "similar"}
			raw, _ := json.Marshal(w)
			intent, err := ParseForPrompt(raw, prompt)
			if err != nil || len(intent.References) != 1 || intent.References[0].Kind != core.ReferenceKind(kind) {
				t.Fatalf("%s %q: %+v %v", kind, value, intent.References, err)
			}
		}
	}
}

func TestQualifiedReferenceCannotInventAnArtistOrTitle(t *testing.T) {
	ref := WireReference{Kind: "track", Value: "Unmentioned Artist - Fixture Title", Explicit: true, Span: "Fixture Title"}
	if referenceGrounded("Fixture Title by Another Artist", ref) {
		t.Fatal("invented artist accepted")
	}
	ref.Value = "Another Artist - Unmentioned Title"
	if referenceGrounded("Fixture Title by Another Artist", ref) {
		t.Fatal("invented title accepted")
	}
	ref.Kind = "artist"
	ref.Value = "Another Artist - Fixture Title"
	if referenceGrounded("Fixture Title by Another Artist", ref) {
		t.Fatal("artist namespace accepted artist/title splitting")
	}
}

func TestQualifiedReferenceExpandsTitleOnlySpanFromActualRequest(t *testing.T) {
	const prompt = "Play music like Bjork's track Fixture Title, with related discoveries"
	w := Wire{Genres: []WirePreference{}, References: []WireReference{{Kind: "track", Value: "Fixture Title by Björk", Explicit: true, Span: "Fixture Title", Influence: "positive"}}, Mode: "similar", TotalCount: 6}
	raw, _ := json.Marshal(w)
	intent, err := ParseForPrompt(raw, prompt)
	if err != nil || len(intent.References) != 1 || intent.References[0].Evidence[0].Text != "Bjork's track Fixture Title" || intent.References[0].Evidence[0].Start != 16 {
		t.Fatalf("%+v %v", intent.References, err)
	}
	w.References[0].Span = "Not in the request"
	raw, _ = json.Marshal(w)
	if _, err := ParseForPrompt(raw, prompt); err == nil {
		t.Fatal("fabricated source span was accepted")
	}
}

func TestQualifiedReferenceRecoversRewrittenIdentitySpan(t *testing.T) {
	const prompt = "Build around Bjork's track Fixture Title, then introduce related discoveries"
	ref := WireReference{Kind: "track", Value: "Fixture Title by Björk", Explicit: true, Span: "Fixture Title by Björk", Influence: "positive"}
	w := Wire{Genres: []WirePreference{}, References: []WireReference{ref}, JourneyWaypoints: []WireReference{ref}, Mode: "similar", TotalCount: 6}
	raw, _ := json.Marshal(w)
	intent, err := ParseForPrompt(raw, prompt)
	if err != nil || len(intent.References) != 1 || intent.References[0].Evidence[0].Text != prompt {
		t.Fatalf("%+v %v", intent.References, err)
	}
	// Source recovery cannot manufacture an absent artist, even if the model
	// repeats the invented identity in its source span.
	if _, err := ParseForPrompt(raw, "Build around Another Artist's track Fixture Title"); err == nil {
		// Unmentioned positive suggestions may instead be discarded entirely.
		got, _ := ParseForPrompt(raw, "Build around Another Artist's track Fixture Title")
		if len(got.References) != 0 {
			t.Fatal("invented artist survived source recovery")
		}
	}
	w.References[0].Span += " that must be included"
	raw, _ = json.Marshal(w)
	if _, err := ParseForPrompt(raw, prompt); err == nil {
		t.Fatal("invented instruction in source span was accepted")
	}
}
