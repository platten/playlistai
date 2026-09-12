package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestSourceCompilerRepairsLostFactsBeforeWireValidation(t *testing.T) {
	tests := []struct {
		prompt string
		check  func(*testing.T, core.MusicIntent)
	}{
		{"Give me 15 relaxing classical pieces, mostly piano and strings, no singing.", func(t *testing.T, m core.MusicIntent) {
			if m.Count != 15 || len(m.Preferences.Instrumentation) != 2 || m.Preferences.Instrumentation[1].Degree != "mostly" || !core.WantsInstrumental(m) {
				t.Fatalf("count/degree/vocals lost: %+v", m)
			}
			for _, c := range m.EssentialCriteria {
				if c.Value == "piano" || c.Value == "strings" || c.Value == "relaxing" {
					t.Fatalf("soft preference became essential: %+v", c)
				}
			}
		}},
		{"15 dark industrial rock tracks like Nine Inch Nails, but less aggressive, no Marilyn Manson, and no screaming.", func(t *testing.T, m core.MusicIntent) {
			var excludedArtist, excludedScreaming bool
			for _, c := range m.HardConstraints {
				excludedArtist = excludedArtist || c.Kind == "exclude_artist" && c.Value == "Marilyn Manson"
				excludedScreaming = excludedScreaming || c.Kind == "exclude_vocal" && c.Value == "screaming"
			}
			if !excludedArtist || !excludedScreaming {
				t.Fatalf("conjoined exclusions lost: %+v", m.HardConstraints)
			}
		}},
		{"Build a 30-minute running playlist that starts easy, picks up the pace, then cools down. I like electronic music, but no harsh vocals.", func(t *testing.T, m core.MusicIntent) {
			if m.DurationSeconds != 1800 || m.Count == 30 || len(m.Journey.EnergyTrajectory) != 3 {
				t.Fatalf("duration or energy arc lost: %+v", m)
			}
		}},
		{"Something like Christian Löffler and Kiasmos for a rainy evening: melancholic but comforting, mostly instrumental. 15 tracks.", func(t *testing.T, m core.MusicIntent) {
			if len(m.References) != 2 || m.References[0].Query != "Christian Löffler" || m.Preferences.VocalPreference == nil || m.Preferences.VocalPreference.Strength != "preferred" || core.WantsInstrumental(m) {
				t.Fatalf("names or soft instrumental lost: %+v", m)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 30, Temporal: []core.TemporalRequirement{{Basis: "original_release", StartYear: 1990, EndYear: 2023}}}
			raw, _ := json.Marshal(w)
			m, err := ParseForPrompt(raw, tt.prompt)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Temporal) != 0 || m.Translation == nil || m.OriginalDescription != tt.prompt {
				t.Fatalf("invented temporal filter or missing provenance: %+v", m)
			}
			tt.check(t, m)
		})
	}
}

func TestSimilarityReferenceCannotBecomeRequiredOutput(t *testing.T) {
	const prompt = "15 songs like Aerosmith, but don't include Aerosmith themselves"
	r := WireReference{Kind: "artist", Value: "Aerosmith", Explicit: true, Influence: "positive", Span: "Aerosmith"}
	w := Wire{Genres: []WirePreference{}, References: []WireReference{r}, RequiredTracks: []WireReference{r}, Destination: []WireReference{r}, Mode: "journey", TotalCount: 15}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || m.Destination != nil || m.Start != nil || len(m.RequiredTracks) != 0 || len(m.References) != 2 {
		t.Fatalf("similarity promoted to output: %+v %v", m, err)
	}
}

func TestUnknownSourceMeaningSurvivesDictionaryReconciliation(t *testing.T) {
	const prompt = "12 tracks of unfamiliar-glasswave music with liquid tessellation"
	w := Wire{Genres: []WirePreference{{Value: "unfamiliar-glasswave", Span: "unfamiliar-glasswave", Explicit: true, Influence: "positive"}}, Textures: []WirePreference{{Value: "liquid tessellation", Span: "liquid tessellation", Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 12}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "unfamiliar-glasswave" || len(m.Preferences.TextureDescriptions) != 1 || !strings.Contains(m.Preferences.TextureDescriptions[0].Value, "tessellation") {
		t.Fatalf("unknown source wording lost: %+v %v", m, err)
	}
}

func TestBroadPromptSpanDoesNotLicenseInventedMandatoryGenres(t *testing.T) {
	const prompt = "15 tracks like Imaginary Artist with ashen shimmer"
	w := Wire{Genres: []WirePreference{{Value: "post-hardcore", Span: prompt, Explicit: true, Influence: "positive"}}, EssentialCriteria: []WireCriterion{{Kind: "genre", Value: "heavy metal", Span: prompt, Scope: "playlist"}}, Textures: []WirePreference{{Value: "ashen shimmer", Span: "ashen shimmer", Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 15}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.EssentialCriteria) != 0 || len(m.Preferences.Genres) != 0 || len(m.Preferences.TextureDescriptions) != 1 || len(m.References) != 1 {
		t.Fatalf("invented genre acquired mandatory status: %+v %v", m, err)
	}
}

func TestLiteralDescriptorCannotAcquireRequiredTrackRole(t *testing.T) {
	const prompt = "10 tracks with ashen shimmer"
	r := WireReference{Kind: "track", Value: "ashen shimmer", Span: "ashen shimmer", Explicit: true, Influence: "positive"}
	w := Wire{Genres: []WirePreference{}, RequiredTracks: []WireReference{r}, Destination: []WireReference{r}, Textures: []WirePreference{{Value: "ashen shimmer", Span: "ashen shimmer", Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 10}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.RequiredTracks) != 0 || m.Destination != nil || len(m.Preferences.TextureDescriptions) != 1 {
		t.Fatalf("unrequested output role survived: %+v %v", m, err)
	}
}

func TestExplicitRequiredTrackRemainsRequired(t *testing.T) {
	const prompt = "12 atmospheric tracks, must include Fixture Artist - Fixture Title"
	r := WireReference{Kind: "track", Value: "Fixture Artist - Fixture Title", Span: "Fixture Artist - Fixture Title", Explicit: true, Influence: "positive"}
	w := Wire{Genres: []WirePreference{}, RequiredTracks: []WireReference{r}, Mode: "similar", TotalCount: 12}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.RequiredTracks) != 1 || m.RequiredTracks[0].Query != r.Value {
		t.Fatalf("real include instruction lost: %+v %v", m, err)
	}
}

func TestOmittedQualifiedIncludeIsRecoveredBeforeModelValidation(t *testing.T) {
	const prompt = `15 classical pieces, must include "Fixture Artist - Rock and Roll 1999"; include "Fixture Artist - Rock and Roll 1999"`
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 20}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || m.Count != 15 || len(m.RequiredTracks) != 1 || m.RequiredTracks[0].Query != "Fixture Artist - Rock and Roll 1999" || len(m.Temporal) != 0 || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "classical" {
		t.Fatalf("omitted include, title shielding or count changed: %+v %v", m, err)
	}
	for _, instruction := range []string{"do not include", "don't include", "never include"} {
		got, parseErr := ParseForPrompt(raw, "15 tracks; "+instruction+" Fixture Artist - Fixture Title")
		if parseErr == nil && len(got.RequiredTracks) != 0 {
			t.Fatalf("negated inclusion became mandatory: %+v", got.RequiredTracks)
		}
	}
}

func TestTypedCalendarAndDurationCannotAlsoBecomeGenres(t *testing.T) {
	for _, phrase := range []string{"20th-century", "30-minute"} {
		prompt := "classical music, " + phrase
		w := Wire{Genres: []WirePreference{{Value: phrase, Span: phrase, Explicit: true, Influence: "positive"}}, EssentialCriteria: []WireCriterion{{Kind: "texture", Value: phrase, Span: phrase, Scope: "playlist"}}, Mode: "similar", TotalCount: 20}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "classical" || len(m.Preferences.Genres) != 1 {
			t.Fatalf("time expression became musical category: %+v %v", m, err)
		}
		if phrase == "20th-century" && (len(m.Temporal) != 1 || m.Temporal[0].StartYear != 1901) {
			t.Fatalf("calendar lost: %+v", m.Temporal)
		}
		if phrase == "30-minute" && m.DurationSeconds != 1800 {
			t.Fatal("duration lost")
		}
	}
}
