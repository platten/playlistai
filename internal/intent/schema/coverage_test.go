package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestSourceCoverageSurvivesModelAndWireProjections(t *testing.T) {
	const prompt = "Give me 10 tracks mixing house, techno, and UK garage."
	w := Wire{Mode: "similar", TotalCount: 10, Genres: []WirePreference{{Value: "house", Span: "house", Explicit: true, Influence: "positive"}}}
	raw, _ := json.Marshal(w)
	source := lexicon.Extract(prompt)
	m, err := ParseForPromptWithSource(raw, prompt, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.EssentialCriteria) != 3 {
		t.Fatalf("coverage members lost: %+v", m)
	}
	for _, c := range m.EssentialCriteria {
		if c.CoverageGroup == "" {
			t.Fatal("model omission erased source coverage")
		}
	}
	compileSource(&w, &source, prompt)
	for _, c := range w.ToCore().EssentialCriteria {
		if c.CoverageGroup == "" {
			t.Fatal("wire projection erased coverage")
		}
	}
	for _, p := range w.ToCore().Preferences.Genres {
		if p.CoverageGroup == "" {
			t.Fatal("preference projection erased coverage")
		}
	}
	// Supplied historical source snapshots retain the meaning recorded then.
	for i := range source.Atoms {
		source.Atoms[i].CoverageGroup = ""
	}
	legacy, err := ParseForPromptWithSource(raw, prompt, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range legacy.EssentialCriteria {
		if c.CoverageGroup != "" {
			t.Fatal("saved snapshot was reinterpreted")
		}
	}
}

func TestModelCannotInventPlaylistCoverage(t *testing.T) {
	for _, prompt := range []string{"10 tracks with house and techno", "10 unfamiliar-glasswave tracks"} {
		w := Wire{Mode: "similar", TotalCount: 10, Genres: []WirePreference{{Value: "unfamiliar-glasswave", Span: "unfamiliar-glasswave", Explicit: true, Influence: "positive", CoverageGroup: "invented"}}}
		raw, _ := json.Marshal(w)
		m, err := ParseForPrompt(raw, prompt)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range m.EssentialCriteria {
			if c.CoverageGroup != "" {
				t.Fatal("model invented collective instruction")
			}
		}
		for _, p := range m.Preferences.Genres {
			if p.CoverageGroup != "" {
				t.Fatal("model invented preference coverage")
			}
		}
	}
	w := Wire{Mode: "similar", TotalCount: 10, EssentialCriteria: []WireCriterion{{Kind: "genre", Value: "unfamiliar-glasswave", Scope: "playlist", Span: "unfamiliar-glasswave", CoverageGroup: "invented"}}}
	raw, _ := json.Marshal(w)
	m, err := ParseForPrompt(raw, "10 unfamiliar-glasswave tracks")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.EssentialCriteria {
		if c.CoverageGroup != "" {
			t.Fatal("missing genres bypassed source-only coverage")
		}
	}
}
