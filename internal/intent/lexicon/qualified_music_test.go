package lexicon_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestQualifiedMusicPreservesSourceNegativeAndReduction(t *testing.T) {
	for _, test := range []struct{ prompt, phrase, clause, degree string }{
		{"No pounding drums; with soft piano.", "pounding drums", "No pounding drums", "plain"},
		{"With soft piano, avoid pounding drums.", "pounding drums", "avoid pounding drums", "plain"},
		{"With less pounding drums and soft piano.", "pounding drums", "less pounding drums", "reduced"},
		{"Not too pounding drums; with soft piano.", "pounding drums", "Not too pounding drums", "reduced"},
		{"No martelé drums; with piano.", "martelé drums", "No martelé drums", "plain"},
		{"No ringing guitar tones; with piano.", "ringing guitar tones", "No ringing guitar tones", "plain"},
		{"Avoid very pounding drums; with piano.", "very pounding drums", "Avoid very pounding drums", "plain"},
	} {
		for _, kind := range []string{"instrumentation", "texture"} {
			for _, span := range []string{test.phrase, test.clause} {
				t.Run(test.prompt+"/"+kind+"/"+span, func(t *testing.T) {
					// The source owns role even when a synthetic model incorrectly
					// describes the exact supplied wording as positive/required.
					wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
					pref := schema.WirePreference{Value: test.phrase, Span: span, Explicit: true, Influence: "positive", Strength: "required", ConceptID: "instrumentation.drums"}
					if kind == "texture" {
						wire.Textures = []schema.WirePreference{pref}
					} else {
						wire.Instrumentation = []schema.WirePreference{pref}
					}
					intent := parseQualifiedWire(t, wire, test.prompt)
					strict := strings.HasPrefix(test.clause, "No ")
					strength := "preferred"
					if strict {
						strength = "required"
					}
					var found bool
					for _, c := range audio.Clauses(intent) {
						if c.Text == "drums" {
							t.Fatalf("qualified request broadened to generic instrument: %+v", c)
						}
						if c.Text == test.phrase {
							found = true
							if c.Kind != kind || !c.Negative || c.Strict != strict || c.Degree != test.degree {
								t.Fatalf("source role or kind changed: %+v", c)
							}
						}
					}
					if !found || (len(intent.Unsupported) > 0) != strict {
						t.Fatalf("exact supplied interpretation lost: %+v", intent)
					}
					var retained bool
					for _, atom := range intent.Translation.Atoms {
						if atom.Value == "drums" {
							t.Fatalf("saved source reintroduces generic instrument: %+v", atom)
						}
						if atom.Value == test.phrase {
							retained = true
							if atom.Kind != kind || atom.ConceptID != "" || atom.Polarity != "negative" || atom.Strength != strength || atom.Degree != test.degree || len(atom.Evidence) != 1 || atom.Evidence[0].Text != test.clause {
								t.Fatalf("saved source contradicts retained preference: %+v", atom)
							}
						}
					}
					if !retained {
						t.Fatal("full qualified source atom missing")
					}
				})
			}
		}
	}
}

func TestQualifiedMusicOmittedSuffixRemainsInUnsupportedClause(t *testing.T) {
	const prompt = "No ringing guitar tones; with piano."
	intent := parseQualifiedWire(t, schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}, prompt)
	if len(intent.Unsupported) != 1 || intent.Unsupported[0].Text != "No ringing guitar tones" {
		t.Fatalf("qualified suffix was truncated: %+v", intent.Unsupported)
	}
	for _, c := range audio.Clauses(intent) {
		if c.Text == "guitar" || c.Text == "ringing guitar tones" && (!c.Strict || !c.Negative) {
			t.Fatalf("unknown phrase became musical assertion: %+v", c)
		}
	}
}

func TestQualifiedMusicPartialInterpretationCannotBroadenExclusion(t *testing.T) {
	for _, test := range []struct{ prompt, partial, full, clause string }{
		{"Absolutely no ringing guitar samples.", "ringing guitar", "ringing guitar samples", "Absolutely no ringing guitar samples"},
		{"Absolutely no very pounding drums.", "pounding drums", "very pounding drums", "Absolutely no very pounding drums"},
	} {
		wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10, Instrumentation: []schema.WirePreference{{Value: test.partial, Span: test.partial, Influence: "negative", Explicit: true}}}
		intent := parseQualifiedWire(t, wire, test.prompt)
		if len(intent.Unsupported) != 1 || intent.Unsupported[0].Text != test.clause || len(intent.HardConstraints) != 1 || intent.HardConstraints[0].Value != test.full {
			t.Fatalf("partial interpretation manufactured broad exclusion: %+v", intent)
		}
		for _, c := range audio.Clauses(intent) {
			if c.Text == test.partial {
				t.Fatalf("partial descriptor became request: %+v", c)
			}
		}
		// A prior partial interpretation must not narrow the shared occurrence
		// and prevent a subsequent exact full description from surviving.
		wire.Textures = []schema.WirePreference{{Value: test.full, Span: test.full, Influence: "negative", Explicit: true}}
		intent = parseQualifiedWire(t, wire, test.prompt)
		found := false
		for _, c := range audio.Clauses(intent) {
			if c.Text == test.partial {
				t.Fatalf("partial request survived beside full phrase: %+v", c)
			}
			found = found || c.Kind == "texture" && c.Text == test.full && c.Strict && c.Negative
		}
		if !found {
			t.Fatalf("full phrase lost after partial proposal: %+v", intent.Preferences)
		}
	}
}

func TestQualifiedMusicOmittedDetailRemainsUnsupported(t *testing.T) {
	for _, path := range []string{"rules", "source", "schema_omitted", "schema_plain", "schema_unsupported"} {
		t.Run(path, func(t *testing.T) {
			const prompt = "No pounding drums; with soft piano."
			var intent core.MusicIntent
			switch path {
			case "rules":
				var err error
				intent, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
				if err != nil {
					t.Fatal(err)
				}
			case "source":
				intent = lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, lexicon.Extract(prompt))
			default:
				wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
				if path == "schema_plain" {
					wire.Instrumentation = []schema.WirePreference{{Value: "drums", Span: "drums", Influence: "positive", Explicit: true}}
				}
				if path == "schema_unsupported" {
					wire.Unsupported = []schema.WireUnsupported{{Text: "No pounding drums", Span: "No pounding drums", Reason: "Qualified exclusion needs interpretation"}}
				}
				intent = parseQualifiedWire(t, wire, prompt)
			}
			for _, c := range audio.Clauses(intent) {
				if strings.Contains(c.Text, "drums") && (c.Text != "pounding drums" || !c.Negative || !c.Strict) {
					t.Fatalf("full exclusion lost its literal role: %+v", c)
				}
			}
			if len(intent.Unsupported) != 1 || intent.Unsupported[0].Text != "No pounding drums" || len(intent.Unsupported[0].Evidence) != 1 || intent.Unsupported[0].Evidence[0].Start != 0 || intent.Unsupported[0].Evidence[0].End != 17 {
				t.Fatalf("exact unsupported requirement lost: %+v", intent.Unsupported)
			}
			for _, a := range intent.Translation.Atoms {
				if strings.Contains(a.Value, "drums") && (a.Value != "pounding drums" || a.Polarity != "negative" || a.Strength != "required") {
					t.Fatalf("translation silently restored generic instrument: %+v", a)
				}
			}
			if len(intent.HardConstraints) != 1 || intent.HardConstraints[0].Value != "pounding drums" {
				t.Fatalf("full hard exclusion lost: %+v", intent.HardConstraints)
			}
		})
	}
}

func TestQualifiedMusicPreservesOccurrenceScopeAndSnapshots(t *testing.T) {
	const prompt = "No pounding drums; with pounding drums."
	source := lexicon.Extract(prompt)
	before, _ := json.Marshal(source)
	// Explicit offsets distinguish identical wording with opposite polarity.
	pref := core.IntentPreference{Value: "pounding drums", Influence: core.InfluencePositive, Explicit: true, Evidence: []core.SourceEvidence{{Text: "pounding drums", Start: 3, End: 17, Explicit: true}}}
	intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt, Preferences: core.SemanticPreferences{Instrumentation: []core.IntentPreference{pref}}}, source).Normalized()
	var negativeDetail, laterDrums bool
	for _, c := range audio.Clauses(intent) {
		negativeDetail = negativeDetail || c.Text == "pounding drums" && c.Negative
		laterDrums = laterDrums || c.Text == "pounding drums" && !c.Negative && c.Essential
	}
	if !negativeDetail || !laterDrums {
		t.Fatalf("another occurrence was suppressed or qualifier lost: %+v", intent.Preferences)
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("source snapshot was mutated")
	}
	raw, _ := json.Marshal(intent)
	var restored core.MusicIntent
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(intent.Translation, restored.Normalized().Translation) || !reflect.DeepEqual(intent.Preferences, restored.Normalized().Preferences) {
		t.Fatal("normalization or saved history reparsed qualified source")
	}
	// Unpositioned repeated wording must not pick the first matching phrase.
	pref.Evidence[0].Start, pref.Evidence[0].End = -1, -1
	ambiguous := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt, Preferences: core.SemanticPreferences{Instrumentation: []core.IntentPreference{pref}}}, source)
	if len(ambiguous.Unsupported) != 1 {
		t.Fatalf("ambiguous phrase was guessed: %+v", ambiguous)
	}
}

func TestQualifiedMusicPreservesDirectAndEmphasizedExclusions(t *testing.T) {
	for _, prompt := range []string{"No drums.", "No pounding drums.", "Absolutely no pounding drums."} {
		value := "pounding drums"
		if prompt == "No drums." {
			value = "drums"
		}
		wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10, Instrumentation: []schema.WirePreference{{Value: value, Span: value, Influence: "negative", Explicit: true}}}
		intent := parseQualifiedWire(t, wire, prompt)
		strict := true
		var found bool
		for _, c := range audio.Clauses(intent) {
			if c.Text == value {
				found = true
				if !c.Negative || c.Strict != strict {
					t.Fatalf("source force changed for %q: %+v", prompt, c)
				}
			}
		}
		if !found || len(intent.HardConstraints) != 1 || intent.HardConstraints[0].Value != value {
			t.Fatalf("strict/ordinary distinction lost for %q: %+v", prompt, intent)
		}
		for _, c := range intent.HardConstraints {
			if value != "drums" && c.Value == "drums" {
				t.Fatalf("qualified hard request broadened to all drums: %+v", c)
			}
		}
	}
}

func TestQualifiedMusicProtectsBoundariesAndIdentity(t *testing.T) {
	for _, prompt := range []string{
		`Music by "Pounding Drums".`,
		`No music by "Pounding Drums".`,
		"No Radiohead; with pounding drums.",
		"No guitars, with pounding drums.",
		"No guitars but pounding drums.",
		"Not only pounding drums, but piano.",
	} {
		source := lexicon.Extract(prompt)
		intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, source)
		for _, u := range intent.Unsupported {
			if strings.Contains(strings.ToLower(u.Text), "pounding") {
				t.Fatalf("unrelated negator or identity captured for %q: %+v", prompt, u)
			}
		}
	}
	const prompt = "No pounding drums."
	source := lexicon.Extract(prompt)
	// Catalog grounding owns the occurrence, even if a caller retains a
	// competing generic musical atom in the source snapshot.
	source.Atoms = append(source.Atoms, core.IntentAtom{ID: "artist", Kind: "exclude_artist", Value: "pounding drums", Scope: "playlist", Polarity: "negative", Strength: "required", Grounding: &core.IdentityGrounding{Provider: "MusicBrainz"}, Evidence: []core.SourceEvidence{{Text: "pounding drums", Start: 3, End: 17, Explicit: true}}})
	intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, source)
	for _, u := range intent.Unsupported {
		if strings.Contains(u.Reason, "qualified musical") {
			t.Fatalf("grounded identity became an unknown musical qualifier: %+v", u)
		}
	}
}

func TestQualifiedMusicStrictJourneyScopeDoesNotBecomeGlobal(t *testing.T) {
	for _, test := range []struct{ prompt, scope string }{
		{"Start with soft piano, ending with absolutely no pounding drums.", "journey_end"},
		{"Start with absolutely no pounding drums, ending with piano.", "journey_start"},
	} {
		wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "journey", TotalCount: 10, Instrumentation: []schema.WirePreference{{Value: "pounding drums", Span: "pounding drums", Influence: "negative", Explicit: true}}}
		intent := parseQualifiedWire(t, wire, test.prompt)
		found := false
		for _, c := range audio.Clauses(intent) {
			if c.Text == "pounding drums" {
				found = true
				if c.Scope != test.scope || !c.Strict || !c.Negative {
					t.Fatalf("qualified stage changed scope/force: %+v", c)
				}
			}
		}
		if !found || len(intent.Unsupported) == 0 {
			t.Fatalf("strict qualified stage was lost: %+v", intent)
		}
		for _, constraint := range intent.HardConstraints {
			if constraint.Value == "pounding drums" || constraint.Value == "drums" {
				t.Fatalf("scoped request became global exclusion: %+v", constraint)
			}
		}
	}
}

func parseQualifiedWire(t *testing.T, wire schema.Wire, prompt string) core.MusicIntent {
	t.Helper()
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}
