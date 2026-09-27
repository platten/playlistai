package lexicon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
)

type literalInstrumentQueryRecorder struct{ texts []string }

func (*literalInstrumentQueryRecorder) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Dimension: 2}
}

func (*literalInstrumentQueryRecorder) EmbedAudio(context.Context, []float32) ([]float32, error) {
	panic("this regression must not analyze audio")
}

func (a *literalInstrumentQueryRecorder) EmbedText(_ context.Context, text string) ([]float32, error) {
	a.texts = append(a.texts, text)
	return []float32{1, 0}, nil
}

func TestLiteralInstrumentationDetailReachesAudioQueries(t *testing.T) {
	for _, test := range []struct{ prompt, detail string }{
		{"Make a 10-track playlist with distorted guitars, pounding drums, and a tense, restless mood.", "pounding drums"},
		{"For café evenings, 10 tracks with soft piano", "soft piano"},
	} {
		prompt, detail := test.prompt, test.detail
		for _, kind := range []string{"instrumentation", "texture"} {
			t.Run(detail+"/"+kind, func(t *testing.T) {
				// Synthetic model output: the native run did not retain its model wire.
				wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
				preference := schema.WirePreference{Value: detail, Span: detail, Explicit: true, Influence: "positive"}
				caption := "Music featuring " + detail + "."
				if kind == "instrumentation" {
					wire.Instrumentation = []schema.WirePreference{preference}
				} else {
					wire.Textures = []schema.WirePreference{preference}
				}
				raw, _ := json.Marshal(wire)
				intent, err := schema.ParseForPrompt(raw, prompt)
				if err != nil {
					t.Fatal(err)
				}
				preferences := intent.Preferences.Instrumentation
				found := false
				for _, p := range preferences {
					if p.Value != detail {
						continue
					}
					found = true
					if p.Influence != core.InfluencePositive || p.Strength != "essential" || p.Scope != "playlist" || p.ConceptID != "" || len(p.Evidence) != 1 {
						t.Fatalf("literal detail changed role: %+v", p)
					}
					e := p.Evidence[0]
					if e.Text != detail || e.Start != strings.Index(prompt, detail) || e.End != e.Start+len(detail) || !e.Explicit {
						t.Fatalf("literal source span changed: %+v", e)
					}
				}
				if !found {
					t.Fatalf("qualified %s detail lost: %+v", kind, preferences)
				}
				analyzer := new(literalInstrumentQueryRecorder)
				vectors, err := audio.EncodeClauseQueries(context.Background(), analyzer, intent)
				if err != nil {
					t.Fatal(err)
				}
				found = false
				for _, vector := range vectors {
					if vector.Clause.Text == detail {
						found = true
						if vector.Clause.Kind != "instrumentation" || vector.Clause.Strict || !vector.Clause.Essential || vector.Clause.Negative {
							t.Fatalf("qualified clause changed kind or force: %+v", vector.Clause)
						}
					}
				}
				if !found || !containsLiteralQuery(analyzer.texts, detail) || !containsLiteralQuery(analyzer.texts, caption) {
					t.Fatalf("qualified wording did not reach text encoder: %+v", analyzer.texts)
				}
			})
		}
	}
}

func TestLiteralInstrumentationCannotBypassSourceOwnership(t *testing.T) {
	for _, test := range []struct {
		name, prompt, value, span string
		start, end                int
	}{
		{"negative", "no pounding drums", "pounding drums", "pounding drums", -1, -1},
		{"strict", "only pounding drums", "pounding drums", "pounding drums", -1, -1},
		{"degree", "mostly pounding drums", "pounding drums", "pounding drums", -1, -1},
		{"quoted artist", `music by "Pounding Drums"`, "Pounding Drums", "Pounding Drums", -1, -1},
		{"artist", "music by Pounding Drums", "Pounding Drums", "Pounding Drums", -1, -1},
		{"paraphrase", "with pounding drums", "thundering drums", "pounding drums", -1, -1},
		{"incorrect span", "with pounding drums", "pounding drums", "pounding drums", 0, 18},
		{"ambiguous occurrence", "pounding drums then pounding drums", "pounding drums", "pounding drums", -1, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := core.MusicIntent{OriginalDescription: test.prompt, Preferences: core.SemanticPreferences{Instrumentation: []core.IntentPreference{{Value: test.value, Explicit: true, Influence: core.InfluencePositive, Evidence: []core.SourceEvidence{{Text: test.span, Start: test.start, End: test.end, Explicit: true}}}}}}
			got := lexicon.Reconcile(original, lexicon.Extract(test.prompt))
			for _, p := range got.Preferences.Instrumentation {
				if p.Value != test.value || p.Influence != core.InfluencePositive {
					continue
				}
				switch test.name {
				case "strict":
					if p.Strength != "required" {
						t.Fatalf("strict source weakened: %+v", p)
					}
				case "degree":
					if p.Degree != "mostly" || p.Strength != "preferred" {
						t.Fatalf("degree changed: %+v", p)
					}
				case "incorrect span", "ambiguous occurrence":
					if len(p.Evidence) != 1 || p.Evidence[0].Start < 0 || test.prompt[p.Evidence[0].Start:p.Evidence[0].End] != p.Evidence[0].Text {
						t.Fatalf("source span not repaired: %+v", p)
					}
				default:
					t.Fatalf("qualified instrumentation bypassed source ownership: %+v", p)
				}
			}
		})
	}
}

func TestLiteralInstrumentationRecoversExplicitOmittedDetail(t *testing.T) {
	const prompt = "10 tracks with pounding drums"
	for _, supplied := range []bool{false, true} {
		wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
		if supplied {
			wire.Instrumentation = []schema.WirePreference{{Value: "drums", Span: "drums", Explicit: true, Influence: "positive"}}
		}
		raw, _ := json.Marshal(wire)
		intent, err := schema.ParseForPrompt(raw, prompt)
		if err != nil {
			t.Fatal(err)
		}
		var drums int
		for _, clause := range audio.Clauses(intent) {
			if clause.Text == "drums" {
				t.Fatalf("defining modifier dropped: %+v", clause)
			}
			if clause.Kind == "instrumentation" && clause.Text == "pounding drums" && clause.Essential {
				drums++
			}
		}
		if drums != 1 {
			t.Fatalf("plain instrument duplicated or lost: %+v", intent.Preferences)
		}
	}
}

func containsLiteralQuery(queries []string, want string) bool {
	for _, query := range queries {
		if query == want {
			return true
		}
	}
	return false
}
