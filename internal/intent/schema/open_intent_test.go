package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestOpenGenreKeepsOriginalRequirementAndDiscardsInventedExpansionRoot(t *testing.T) {
	w := Wire{Genres: []WirePreference{{Value: "未知ジャンル", Explicit: true, Span: "未知ジャンル", Influence: "positive"}}, Mode: "similar", TotalCount: 5,
		GenreExpansions: []core.GenreExpansion{{Genre: "unrequested category", Characteristics: "invented replacement", RelatedGenres: []string{"electronic"}}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "未知ジャンル")
	if err != nil || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "未知ジャンル" || len(m.GenreExpansions) != 0 {
		t.Fatalf("original category lost: %+v %v", m, err)
	}
}

func TestOpenIntentSeparatesEntityNamespacesAndExclusions(t *testing.T) {
	for _, kind := range []string{"artist", "album", "track"} {
		t.Run(kind, func(t *testing.T) {
			w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5, References: []WireReference{{Kind: kind, Value: "Common Name", Explicit: true, Span: "Common Name", Influence: "negative"}}}
			raw, _ := json.Marshal(w)
			if _, err := ParseForPrompt(raw, "No "+kind+" Common Name"); err == nil {
				t.Fatal("negative entity without exclusion accepted")
			}
			w.HardConstraints = []WireConstraint{{Kind: "exclude_" + kind, Value: "Common Name", Span: "No " + kind + " Common Name"}}
			raw, _ = json.Marshal(w)
			m, err := ParseForPrompt(raw, "No "+kind+" Common Name")
			if err != nil || len(m.References) != 1 || string(m.References[0].Kind) != kind {
				t.Fatalf("namespace lost: %+v %v", m, err)
			}
		})
	}
}

func TestOpenIntentRejectsFabricatedSourceAndUnqualifiedTrack(t *testing.T) {
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5, Moods: []WirePreference{{Value: "energetic", Explicit: true, Span: "unmentioned"}}}
	raw, _ := json.Marshal(w)
	if _, err := ParseForPrompt(raw, "lively music"); err == nil {
		t.Fatal("invented source span accepted")
	}
	w.Moods = nil
	w.InferredAnchors = []WireAnchor{{Kind: "track", Value: "Ambiguous title", Role: "retrieval", Reason: "possible fit"}}
	raw, _ = json.Marshal(w)
	if m, err := ParseForPrompt(raw, "lively music"); err != nil || len(m.InferredAnchors) != 0 {
		t.Fatal("unqualified optional proposal was not discarded")
	}
}

func TestClassicalCenturyAndDestinationPreserveStageScope(t *testing.T) {
	for _, test := range []struct {
		century     string
		first, last int
	}{{"19th", 1801, 1900}, {"20th", 1901, 2000}, {"21st", 2001, 2100}} {
		prompt := "classical music from the " + test.century + " century transitioning to Artist at the end"
		w := Wire{Genres: []WirePreference{{Value: "classical", Explicit: true, Span: "classical", Influence: "positive"}}, Mode: "journey", TotalCount: 5,
			Destination:      []WireReference{{Kind: "artist", Value: "Artist", Explicit: true, Influence: "positive", Span: "Artist"}},
			JourneyWaypoints: []WireReference{{Kind: "artist", Value: "Invented Composer", Explicit: true, Influence: "positive", Span: "classical"}},
			HardConstraints:  []WireConstraint{{Kind: "energy_trajectory", Value: "ascending", Span: "transitioning to Artist"}}}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil || len(m.Temporal) != 1 || m.Temporal[0].Basis != "composition" || m.Temporal[0].StartYear != test.first || m.Temporal[0].EndYear != test.last || m.Temporal[0].Scope != "journey_start" || len(m.HardConstraints) != 0 || len(m.Journey.Waypoints) != 1 {
			t.Fatalf("stage meaning lost: %+v %v", m, err)
		}
	}
}

func TestUnrepresentedQualityClausePreservesOpenVocabulary(t *testing.T) {
	for _, quality := range []string{"dynamics", "microdynamics", "shimmering spectral detail", "unfamiliar sonic quality"} {
		prompt := "music with lots of " + quality + " transitioning to Artist"
		w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil || len(m.Preferences.TextureDescriptions) != 1 || m.Preferences.TextureDescriptions[0].Value != quality {
			t.Fatalf("quality lost: %+v %v", m, err)
		}
	}
}

func TestInstrumentalInstrumentationNormalizesToVocalPreference(t *testing.T) {
	w := Wire{Genres: []WirePreference{}, Mode: "similar", TotalCount: 5, Instrumentation: []WirePreference{{Value: "instrumental", Influence: "positive", Explicit: true, Span: "instrumental"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "instrumental music")
	if err != nil || m.Preferences.VocalPreference == nil || m.Preferences.VocalPreference.Value != "instrumental" || len(m.Preferences.Instrumentation) != 0 {
		t.Fatalf("instrumental meaning lost: %+v %v", m, err)
	}
}
