package lexicon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

// Run the same source regressions through both the deterministic fallback and
// model reconciliation. The model deliberately contributes no useful identity
// hints: explicit syntax must remain sufficient even after a poor model parse.
func eachCompiler(t *testing.T, prompt string, check func(*testing.T, core.MusicIntent)) {
	t.Helper()
	t.Run("rules", func(t *testing.T) {
		m, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		check(t, m)
	})
	t.Run("model", func(t *testing.T) {
		wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
		raw, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		m, err := schema.ParseForPrompt(raw, prompt)
		if err != nil {
			t.Fatal(err)
		}
		check(t, m)
	})
}

func TestEvaluationQuotedReferences(t *testing.T) {
	tests := []struct {
		prompt  string
		queries []string
	}{
		{"Give me 10 songs like 'Teardrop' by Massive Attack.", []string{"Teardrop by Massive Attack"}},
		{"Make a 10-song playlist inspired by 'So What' by Miles Davis and 'Take Five' by the Dave Brubeck Quartet.", []string{"So What by Miles Davis", "Take Five by the Dave Brubeck Quartet"}},
		{"Like “Scarborough Fair” by Simon and Garfunkel and ‘Down by the Water’ by PJ Harvey, 10 tracks.", []string{"Scarborough Fair by Simon and Garfunkel", "Down by the Water by PJ Harvey"}},
		{`Like "Warm and Keep Going" by Earth, Wind & Fire.`, []string{"Warm and Keep Going by Earth, Wind & Fire"}},
		{`Like 'Don't Stop' by Fleetwood Mac.`, []string{"Don't Stop by Fleetwood Mac"}},
	}
	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			eachCompiler(t, tt.prompt, func(t *testing.T, m core.MusicIntent) {
				if len(m.References) != len(tt.queries) {
					t.Fatalf("references: %+v", m.References)
				}
				for i, r := range m.References {
					if r.Kind != core.ReferenceTrack || r.Query != tt.queries[i] {
						t.Fatalf("reference %d: %+v", i, r)
					}
					if len(r.Evidence) == 0 || !strings.Contains(tt.prompt, r.Evidence[0].Text) {
						t.Fatalf("missing source evidence: %+v", r)
					}
				}
				if len(m.RequiredTracks) != 0 {
					t.Fatalf("similarity promoted to required: %+v", m.RequiredTracks)
				}
			})
		})
	}
}

func TestGroundedWholeArtistSurvivesSpeculativeAndSplit(t *testing.T) {
	prompt := "Give me music like Simon and Garfunkel."
	w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10, References: []schema.WireReference{{Kind: "artist", Value: "Simon and Garfunkel", Span: "Simon and Garfunkel", Influence: "positive", Explicit: true}}}
	raw, _ := json.Marshal(w)
	m, err := schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.References) != 1 || m.References[0].Query != "Simon and Garfunkel" {
		t.Fatalf("full artist split: %+v", m.References)
	}
	// A model cannot protect an unmentioned name through a broad/fabricated span.
	w.References[0].Value = "Simon and Another Artist"
	raw, _ = json.Marshal(w)
	m, err = schema.ParseForPrompt(raw, prompt)
	if err == nil {
		for _, r := range m.References {
			if strings.Contains(r.Query, "Another Artist") {
				t.Fatalf("invented artist survived: %+v", r)
			}
		}
	}
}

func TestPossessiveApostrophesDoNotMaskMusicalEvidence(t *testing.T) {
	x := lexicon.Extract("From Brian Eno's ambient sound to Jon Hopkins's electronic style.")
	genres := map[string]bool{}
	for _, a := range x.Atoms {
		if a.Kind == "genre" {
			genres[a.Value] = true
		}
	}
	if !genres["ambient"] || !genres["electronic"] {
		t.Fatalf("apostrophes hid genres: %+v", x.Atoms)
	}
}

func TestEvaluationCompoundGenres(t *testing.T) {
	for _, genre := range []string{"heavy metal", "Japanese city pop", "Brazilian jazz", "folk rock", "melodic house"} {
		t.Run(genre, func(t *testing.T) {
			eachCompiler(t, "Give me 10 "+genre+" songs.", func(t *testing.T, m core.MusicIntent) {
				if len(m.Preferences.Genres) != 1 || m.Preferences.Genres[0].Value != strings.ToLower(genre) {
					t.Fatalf("compound lost: %+v", m.Preferences.Genres)
				}
				if len(m.References) != 0 {
					t.Fatalf("genre became artist: %+v", m.References)
				}
			})
		})
	}
}

func TestEvaluationMultipleArtists(t *testing.T) {
	for _, tt := range []struct {
		prompt  string
		artists []string
	}{
		{"Make a 10-song playlist around Nina Simone and Bill Withers.", []string{"Nina Simone", "Bill Withers"}},
		{"Give me 10 tracks inspired by Kraftwerk, Tangerine Dream, and Brian Eno.", []string{"Kraftwerk", "Tangerine Dream", "Brian Eno"}},
		{"Make a 10-song playlist inspired by Fela Kuti and Tony Allen, with a variety of artists.", []string{"Fela Kuti", "Tony Allen"}},
	} {
		t.Run(tt.prompt, func(t *testing.T) {
			eachCompiler(t, tt.prompt, func(t *testing.T, m core.MusicIntent) {
				if len(m.References) != len(tt.artists) {
					t.Fatalf("reference count: %+v", m.References)
				}
				for i, name := range tt.artists {
					if m.References[i].Query != name {
						t.Fatalf("wrong artist: %+v", m.References[i])
					}
				}
			})
		})
	}
}

func TestEvaluationJourneyStages(t *testing.T) {
	prompt := "Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock."
	eachCompiler(t, prompt, func(t *testing.T, m core.MusicIntent) {
		if m.Mode != core.ModeJourney {
			t.Fatalf("lost journey: %+v", m)
		}
		want := map[string]string{"acoustic folk": "journey_start", "folk rock": "journey_via", "alternative rock": "journey_end"}
		for _, p := range m.Preferences.Genres {
			if want[p.Value] != p.Scope {
				t.Fatalf("wrong scope: %+v", p)
			}
			delete(want, p.Value)
		}
		if len(want) != 0 || len(m.References) != 0 {
			t.Fatalf("lost category or spurious artist: %+v %+v", want, m.References)
		}
	})
}

func TestEvaluationPossessiveJourney(t *testing.T) {
	prompt := "Make a 10-track journey from Brian Eno's ambient sound through Boards of Canada to Jon Hopkins, including other artists along the way."
	eachCompiler(t, prompt, func(t *testing.T, m core.MusicIntent) {
		if m.Start == nil || m.Start.Query != "Brian Eno" || m.Destination == nil || m.Destination.Query != "Jon Hopkins" {
			t.Fatalf("bad endpoints: %+v / %+v; refs %+v", m.Start, m.Destination, m.References)
		}
		found := false
		for _, p := range m.Preferences.Genres {
			found = found || p.Value == "ambient" && p.Scope == "journey_start"
		}
		if !found {
			t.Fatalf("ambient source lost: %+v", m.Preferences.Genres)
		}
		if !core.RequiresOtherArtists(m) {
			t.Fatalf("other artists lost: %+v", m.HardConstraints)
		}
	})
}

func TestOtherArtistsRequiresAffirmativeSource(t *testing.T) {
	eachCompiler(t, "Give me 10 songs similar to Radiohead including other artists.", func(t *testing.T, m core.MusicIntent) {
		if !core.RequiresOtherArtists(m) || len(m.References) != 1 || m.References[0].Query != "Radiohead" {
			t.Fatalf("additive requirement swallowed by artist: %+v / %+v", m.References, m.HardConstraints)
		}
	})
	for _, prompt := range []string{"Like Radiohead, do not include other artists.", "Like Radiohead, never include other artists.", `Like "Including Other Artists".`, "Only Radiohead."} {
		eachCompiler(t, prompt, func(t *testing.T, m core.MusicIntent) {
			if core.RequiresOtherArtists(m) {
				t.Fatalf("invented requirement: %+v", m.HardConstraints)
			}
		})
	}
	m := lexicon.Reconcile(core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: core.HardConstraintIncludeOtherArtists, Value: "true"}}}, lexicon.Extract("Like Radiohead."))
	if core.RequiresOtherArtists(m) {
		t.Fatal("ungrounded model requirement survived")
	}
}
