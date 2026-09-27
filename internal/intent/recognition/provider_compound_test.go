package recognition

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
)

func TestProviderCompoundPreservesLiteralGenreAndSeparateLists(t *testing.T) {
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "indie"}, {Name: "indie pop"}, {Name: "pop"}}}
	for _, test := range []struct {
		prompt string
		want   []string
	}{
		{"Björk、穏やか。10 indie-pop songs.", []string{"indie pop"}},
		{"10 indie‐pop songs.", []string{"indie pop"}},
		{"10 indie pop songs.", []string{"indie pop"}},
		{"10 deep house songs.", []string{"deep house"}},
		{"10 indie and pop songs.", []string{"indie", "pop"}},
		{"10 spectral-pop songs.", nil},
	} {
		t.Run(test.prompt, func(t *testing.T) {
			source := Apply(context.Background(), test.prompt, lexicon.Extract(test.prompt), nil, vocabulary)
			var values []string
			for _, atom := range source.Atoms {
				if atom.Kind != "genre" {
					continue
				}
				values = append(values, atom.Value)
				for _, evidence := range atom.Evidence {
					if test.prompt[evidence.Start:evidence.End] != evidence.Text || !evidence.Explicit {
						t.Fatalf("source byte offsets changed: %+v", evidence)
					}
					if atom.Value == "indie pop" && !strings.Contains(evidence.Text, "indie") {
						t.Fatalf("compound lost literal source: %+v", evidence)
					}
				}
			}
			if !reflect.DeepEqual(values, test.want) {
				t.Fatalf("genres=%q want=%q", values, test.want)
			}
			raw, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10})
			intent, err := schema.ParseForPromptWithSource(raw, test.prompt, source)
			if err != nil {
				t.Fatal(err)
			}
			var criteria []string
			for _, criterion := range intent.EssentialCriteria {
				if criterion.Kind == "genre" {
					criteria = append(criteria, criterion.Value)
				}
			}
			if !reflect.DeepEqual(criteria, test.want) {
				t.Fatalf("compiled criteria=%q want=%q", criteria, test.want)
			}
		})
	}
}

func TestProviderGenreCannotBroadenDefiningDescription(t *testing.T) {
	const prompt = "Give me 10 songs with jangly guitars, melodic bass lines, and bright indie-pop energy."
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "indie"}, {Name: "indie pop"}, {Name: "pop"}}}
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), nil, vocabulary)
	raw, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10})
	intent, err := schema.ParseForPromptWithSource(raw, prompt, source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"jangly guitars": "instrumentation", "melodic bass lines": "instrumentation", "bright indie-pop energy": "texture"}
	for _, criterion := range intent.EssentialCriteria {
		kind, ok := want[criterion.Value]
		if !ok || criterion.Kind != kind || criterion.ConceptID != "" || criterion.Strength != "essential" || criterion.Scope != "playlist" || len(criterion.Evidence) != 1 {
			t.Fatalf("literal musical requirement broadened or lost source force: %+v", criterion)
		}
		evidence := criterion.Evidence[0]
		if evidence.Text != criterion.Value || prompt[evidence.Start:evidence.End] != criterion.Value || !evidence.Explicit {
			t.Fatalf("literal evidence lost: %+v", evidence)
		}
		delete(want, criterion.Value)
	}
	if len(want) != 0 || len(intent.Preferences.Genres) != 0 {
		t.Fatalf("missing full descriptions or invented broad genre: missing=%v intent=%+v", want, intent)
	}
}

func TestProviderCompoundDoesNotOverrideSourceTexture(t *testing.T) {
	const prompt = "10 warm house songs"
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "warm house"}}}
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), nil, vocabulary)
	var genres, textures []string
	for _, atom := range source.Atoms {
		switch atom.Kind {
		case "genre":
			genres = append(genres, atom.Value)
		case "texture":
			textures = append(textures, atom.Value)
		}
	}
	// A new provider phrase cannot reclassify an independently recognized
	// texture. Reviewed compounds such as deep house are already extracted whole.
	if !reflect.DeepEqual(genres, []string{"house"}) || !reflect.DeepEqual(textures, []string{"warm"}) {
		t.Fatalf("provider overrode source roles: genres=%q textures=%q", genres, textures)
	}
}

func TestProviderCompoundDistinguishesExplicitQuotedGenreFromReferences(t *testing.T) {
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "indie"}, {Name: "indie pop"}, {Name: "pop"}}}
	store := recognitionStore(t)
	defer store.Close()
	for _, test := range []struct {
		prompt string
		want   []string
	}{
		{`10 songs in the genre "indie pop".`, []string{"indie pop"}},
		{`10 songs in the genre “indie-pop”.`, []string{"indie pop"}},
		{`10 songs in the genre 'indie pop'.`, []string{"indie pop"}},
		{`10 songs in the genres "indie pop" and "country".`, []string{"indie pop", "country"}},
		{`10 songs in the genres "indie pop", "country".`, []string{"indie pop", "country"}},
		{`10 songs in the genres "indie pop" or "country".`, []string{"indie pop", "country"}},
		{`10 songs like "Indie Pop" by Low.`, nil},
		{`10 songs by "Indie Pop".`, nil},
		{`10 songs with "indie pop".`, nil},
	} {
		for _, lookup := range []IdentityLookup{nil, store} {
			t.Run(test.prompt, func(t *testing.T) {
				source := Apply(context.Background(), test.prompt, lexicon.Extract(test.prompt), lookup, vocabulary)
				raw, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10})
				intent, err := schema.ParseForPromptWithSource(raw, test.prompt, source)
				if err != nil {
					t.Fatal(err)
				}
				var criteria []string
				for _, criterion := range intent.EssentialCriteria {
					if criterion.Kind == "genre" {
						criteria = append(criteria, criterion.Value)
					}
				}
				if !reflect.DeepEqual(criteria, test.want) {
					t.Fatalf("quoted genre criteria=%q want=%q; atoms=%+v", criteria, test.want, source.Atoms)
				}
				if len(intent.EssentialCriteria) == 2 {
					left, right := intent.EssentialCriteria[0], intent.EssentialCriteria[1]
					if strings.Contains(test.prompt, " or ") {
						if left.Group == "" || left.Group != right.Group {
							t.Fatalf("quoted genre alternative lost: %+v", intent.EssentialCriteria)
						}
					} else if left.Group != "" || right.Group != "" {
						t.Fatalf("quoted independent genres merged: %+v", intent.EssentialCriteria)
					}
				}
			})
		}
	}
}

func TestProviderCompoundKeepsLogicalRolesAndProtectedReferences(t *testing.T) {
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "indie"}, {Name: "indie pop"}}}
	for _, prompt := range []string{"indie-pop or country", "spanning indie-pop and country", "indie and pop", "no indie-pop"} {
		source := lexicon.WithGenreCoverage(Apply(context.Background(), prompt, lexicon.Extract(prompt), nil, vocabulary))
		var genres []core.IntentAtom
		for _, atom := range source.Atoms {
			if atom.Kind == "genre" {
				genres = append(genres, atom)
			}
		}
		switch prompt {
		case "indie-pop or country":
			if len(genres) != 2 || genres[0].Group == "" || genres[0].Group != genres[1].Group {
				t.Fatalf("OR lost: %+v", genres)
			}
		case "spanning indie-pop and country":
			if len(genres) != 2 || genres[0].CoverageGroup == "" || genres[0].CoverageGroup != genres[1].CoverageGroup {
				t.Fatalf("collective coverage lost: %+v", genres)
			}
		case "indie and pop":
			if len(genres) != 2 || genres[0].Group != "" || genres[1].Group != "" || genres[0].CoverageGroup != "" || genres[1].CoverageGroup != "" {
				t.Fatalf("separate conjunction changed: %+v", genres)
			}
		case "no indie-pop":
			if len(genres) != 1 || genres[0].Value != "indie pop" || genres[0].Polarity != "negative" || genres[0].Strength != "required" {
				t.Fatalf("negative compound changed: %+v", genres)
			}
		}
	}
	store := recognitionStore(t)
	defer store.Close()
	prompt := `songs like "indie-pop" by Low`
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, vocabulary)
	for _, atom := range source.Atoms {
		if atom.Kind == "genre" {
			t.Fatalf("quoted reference became genre: %+v", atom)
		}
	}
}
