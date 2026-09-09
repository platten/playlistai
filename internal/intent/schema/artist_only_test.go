package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestArtistOnlyContractAcrossParsers(t *testing.T) {
	const prompt = "Make a playlist only by Radiohead, 10 songs."
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10}
	raw, _ := json.Marshal(w)
	model, err := ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	for name, intent := range map[string]core.MusicIntent{"model": model, "rules": fallback} {
		only := false
		for _, c := range intent.HardConstraints {
			only = only || c.Kind == "require_artist" && c.Value == "Radiohead"
		}
		if len(intent.References) != 1 || intent.References[0].Query != "Radiohead" || intent.Controls.TotalTrackCount != 10 || !only || intent.Constraints.NoRepeatArtistBackToBack {
			t.Fatal(name, intent)
		}
	}
	if rules.OnlyArtist("10 songs like Radiohead") != "" {
		t.Fatal("similarity turned into artist-only")
	}
	conflict, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt + " No back-to-back artists."})
	if err != nil || !conflict.Constraints.NoRepeatArtistBackToBack {
		t.Fatal("artist-only exception removed explicit artist spacing", err)
	}
}

func TestRepeatedCountIsAControlNotAnUnsupportedMusicalDemand(t *testing.T) {
	for _, prompt := range []string{"Make a playlist, 10 songs.", "Make a 10-song playlist.", "Make a 10-track orchestral playlist.", "10 R&B songs", "10 曲のプレイリスト"} {
		for _, value := range []string{"10", "11", ",", "value"} {
			w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 10,
				HardConstraints: []WireConstraint{{Kind: "count", Value: value, Span: prompt}}}
			raw, _ := json.Marshal(w)
			m, err := ParseForPrompt(raw, prompt)
			if err != nil {
				t.Fatal(err)
			}
			if m.Controls.TotalTrackCount != 10 || len(m.HardConstraints) != 0 {
				t.Fatalf("value=%s intent=%+v", value, m)
			}
		}
	}
}
