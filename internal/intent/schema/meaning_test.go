package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestRejectInventedAndConflictingEntityExclusions(t *testing.T) {
	for _, prompt := range []string{
		"classical music ending with Miles Davis",
		"classical music ending with Miles Davis but no Miles Davis",
		"not only Miles Davis but also John Coltrane",
	} {
		w := Wire{Genres: []WirePreference{}, Mode: "journey", TotalCount: 5,
			Destination:     []WireReference{{Kind: "artist", Value: "Miles Davis", Span: "Miles Davis", Explicit: true, Influence: "positive"}},
			HardConstraints: []WireConstraint{{Kind: "exclude_artist", Value: "Miles Davis", Span: "Miles Davis"}}}
		raw, _ := json.Marshal(w)
		if _, err := ParseForPrompt(raw, prompt); err == nil {
			t.Fatalf("invalid exclusion accepted: %q", prompt)
		}
	}
}

func TestExclusionListsAndReferenceOnlyOppositionRemainValid(t *testing.T) {
	prompt := "songs like Skrillex but no Skrillex or Bassnectar"
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5,
		References:      []WireReference{{Kind: "artist", Value: "Skrillex", Span: "Skrillex", Explicit: true, Influence: "positive"}},
		HardConstraints: []WireConstraint{{Kind: "exclude_artist", Value: "Skrillex", Span: "Skrillex"}, {Kind: "exclude_artist", Value: "Bassnectar", Span: "Bassnectar"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.HardConstraints) != 2 {
		t.Fatalf("legitimate exclusions lost: %+v %v", m, err)
	}
	for _, c := range m.HardConstraints {
		if len(c.Evidence) != 1 || c.Evidence[0].Text != "no Skrillex or Bassnectar" {
			t.Fatalf("exclusion lost polarity evidence: %+v", c)
		}
	}
}

func TestEmotionPreservesTypePolarityAndScope(t *testing.T) {
	for _, prompt := range []string{"romantic music with female vocals", "not romantic music"} {
		w := Wire{Genres: []WirePreference{{Value: "romantic", Span: "romantic", Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 5,
			EssentialCriteria: []WireCriterion{{Kind: "genre", Value: "romantic", Span: "romantic", Scope: "playlist"}}}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil || len(m.Preferences.Genres) != 0 || len(m.Preferences.Moods) != 1 {
			t.Fatalf("emotion became a genre: %+v %v", m, err)
		}
		if prompt == "not romantic music" && m.Preferences.Moods[0].Influence != core.InfluenceNegative {
			t.Fatal("negation lost")
		}
		if prompt == "not romantic music" && len(m.EssentialCriteria) != 0 {
			t.Fatal("negative mood also required")
		}
		if prompt != "not romantic music" && (len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Kind != "mood") {
			t.Fatal("essential mood kind lost")
		}
	}
	w := Wire{Genres: []WirePreference{}, Mode: "journey", TotalCount: 5,
		EssentialCriteria: []WireCriterion{{Kind: "mood", Value: "dreamy", Span: "dreamy", Scope: "journey_end"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "start with jazz and end dreamy")
	if err != nil || len(m.Preferences.Moods) != 0 || m.EssentialCriteria[0].Scope != "journey_end" {
		t.Fatalf("stage mood flattened: %+v %v", m, err)
	}
}

func TestRepairSingleExplicitPeriodDespiteInventedDuplicateRanges(t *testing.T) {
	prompt := "classical music from the 20th century transitioning to Miles Davis"
	w := Wire{Genres: []WirePreference{}, Mode: "journey", TotalCount: 5,
		Destination: []WireReference{{Kind: "artist", Value: "Miles Davis", Span: "Miles Davis", Explicit: true, Influence: "positive"}},
		Temporal:    []core.TemporalRequirement{{Basis: "composition", StartYear: 1900, EndYear: 2000, Scope: "playlist"}, {Basis: "composition", StartYear: 1900, EndYear: 2000, Scope: "journey_end"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, prompt)
	if err != nil || len(m.Temporal) != 1 || m.Temporal[0].StartYear != 1901 || m.Temporal[0].Scope != "journey_start" {
		t.Fatalf("period scope corrupted: %+v %v", m, err)
	}
	w = Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5,
		EssentialCriteria: []WireCriterion{{Kind: "texture", Value: "1990s", Span: "1990s", Scope: "playlist"}}}
	raw, _ = json.Marshal(w)
	m, err = ParseForPrompt(raw, "EDM bangers from the 1990s")
	if err != nil || len(m.Temporal) != 1 || m.Temporal[0].StartYear != 1990 || m.Temporal[0].EndYear != 1999 || len(m.EssentialCriteria) != 0 {
		t.Fatalf("decade became sound: %+v %v", m, err)
	}
}

func TestEssentialQualityAlsoRetainsPreferenceWithoutDemotion(t *testing.T) {
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5,
		EssentialCriteria: []WireCriterion{{Kind: "texture", Value: "microdynamics", Span: "microdynamics", Scope: "playlist"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "electronic music with a good microdynamics")
	if err != nil || len(m.EssentialCriteria) == 0 || len(m.Preferences.TextureDescriptions) != 1 || m.Preferences.TextureDescriptions[0].Value != "microdynamics" {
		t.Fatalf("quality role lost: %+v %v", m, err)
	}
}

func TestEmotionalRepairKeepsIndependentOccurrences(t *testing.T) {
	w := Wire{EssentialCriteria: []WireCriterion{{Kind: "mood", Value: "dreamy", Span: "start dreamy", Scope: "journey_start"}, {Kind: "mood", Value: "dreamy", Span: "end not dreamy", Scope: "journey_end"}}}
	preserveEmotionalMeaning(&w, "start dreamy, end not dreamy")
	if len(w.EssentialCriteria) != 2 || len(w.Moods) != 0 {
		t.Fatalf("independent occurrences changed: %+v", w)
	}
	w = Wire{Moods: []WirePreference{{Value: "romantic", Span: "romantic", Explicit: true, Influence: "positive"}}}
	preserveEmotionalMeaning(&w, "not romantic music")
	if len(w.Moods) != 1 || w.Moods[0].Influence != "negative" {
		t.Fatalf("opposite mood added: %+v", w.Moods)
	}
}

func TestAffirmativeContrastCannotDisappearBehindExclusions(t *testing.T) {
	for _, phrase := range []string{"zouk", "glasslike granular textures", "Björk"} {
		prompt := phrase + " but with no Enya"
		w := Wire{}
		if validateAffirmativeContrast(w, prompt) == nil {
			t.Fatalf("omission accepted: %s", prompt)
		}
		w.Textures = []WirePreference{{Value: phrase, Span: phrase, Influence: "positive", Explicit: true}}
		if err := validateAffirmativeContrast(w, prompt); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateAffirmativeContrast(Wire{}, "anything but no Enya"); err != nil {
		t.Fatal(err)
	}
}

func TestPeriodRepairPreservesCompositionVersusRelease(t *testing.T) {
	for _, tc := range []struct {
		prompt, basis string
		start, end    int
	}{
		{"classical compositions from the 1990s", "composition", 1990, 1999},
		{"21st century recordings of classical compositions", "original_release", 2001, 2100},
	} {
		w := Wire{Temporal: []core.TemporalRequirement{{Basis: tc.basis, Scope: "playlist", StartYear: tc.start, EndYear: tc.end}}}
		normalizePeriods(&w, tc.prompt)
		if len(w.Temporal) != 1 || w.Temporal[0].Basis != tc.basis || w.Temporal[0].StartYear != tc.start || w.Temporal[0].EndYear != tc.end {
			t.Fatalf("basis changed: %+v", w.Temporal)
		}
	}
}

func TestEmotionalRepairPreservesInterpretedReduction(t *testing.T) {
	w := Wire{Moods: []WirePreference{{Value: "energetic", Span: "less energetic", Influence: "negative", Explicit: true}}}
	preserveEmotionalMeaning(&w, "less energetic music")
	if len(w.Moods) != 1 || w.Moods[0].Influence != "negative" {
		t.Fatalf("reduction inverted: %+v", w.Moods)
	}
}

func TestExcludedDecadeIsNotPromotedAndCountContrastNeedsNoDescription(t *testing.T) {
	w := Wire{}
	normalizePeriods(&w, "music but not from the 1990s")
	if len(w.Temporal) != 0 {
		t.Fatalf("excluded decade became required: %+v", w.Temporal)
	}
	for _, prompt := range []string{"give me 20 tracks but no Enya", "make a playlist of ten songs but without Enya"} {
		if err := validateAffirmativeContrast(Wire{TotalCount: 20}, prompt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNegativeOnlyContrastDoesNotInventAffirmativeMeaning(t *testing.T) {
	w := Wire{Instrumentation: []WirePreference{{Value: "drums", Span: "no drums", Influence: "negative", Explicit: true}}, HardConstraints: []WireConstraint{{Kind: "exclude_artist", Value: "Enya", Span: "no Enya"}}}
	if err := validateAffirmativeContrast(w, "no drums but no Enya"); err != nil {
		t.Fatal(err)
	}
}

func TestVocalPreferenceCannotBecomeGenreGate(t *testing.T) {
	w := Wire{Genres: []WirePreference{{Value: "female vocals", Span: "female vocals", Explicit: true, Influence: "positive"}}, VocalPreference: WirePreference{Value: "female vocals", Span: "female vocals", Explicit: true, Influence: "positive"}, EssentialCriteria: []WireCriterion{{Kind: "genre", Value: "female vocals", Span: "female vocals", Scope: "playlist"}}}
	preserveVocalMeaning(&w)
	if len(w.Genres) != 0 || len(w.EssentialCriteria) != 1 || w.EssentialCriteria[0].Kind != "vocal" {
		t.Fatalf("voice became genre: %+v", w)
	}
}

func TestExclusionBoundaryPreservesNamedPunctuationAndPositiveClauses(t *testing.T) {
	for _, name := range []string{"G. Love", "Everything But The Girl"} {
		if exclusionSpan("no "+name, name) == "" {
			t.Fatalf("name split: %s", name)
		}
	}
	for _, prompt := range []string{"no Enya. Play Björk", "no Enya, music by Björk"} {
		if exclusionSpan(prompt, "Björk") != "" {
			t.Fatalf("positive artist excluded: %s", prompt)
		}
	}
}

func TestExclusionListsProtectAllNamedBoundaries(t *testing.T) {
	for _, name := range []string{"G. Love", "Everything But The Girl"} {
		w := Wire{HardConstraints: []WireConstraint{{Kind: "exclude_artist", Value: name, Span: name}, {Kind: "exclude_artist", Value: "Enya", Span: "Enya"}}}
		prompt := "no " + name + " or Enya, music by Björk"
		if err := validateConstraintMeaning(&w, prompt); err != nil {
			t.Fatal(err)
		}
		if exclusionSpan(prompt, "Björk", name, "Enya") != "" {
			t.Fatal("positive continuation became exclusion")
		}
	}
}
