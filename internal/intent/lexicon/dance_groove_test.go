package lexicon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/recognition"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
)

func TestDanceGrooveIsTextureThroughRulesAndSourceAwareModel(t *testing.T) {
	for _, prompt := range []string{
		"Make a 10-song playlist with syncopated bass, lively percussion, and a celebratory dance groove.",
		"For café evenings, give me 10 songs with dance grooves.",
	} {
		t.Run(prompt, func(t *testing.T) {
			source := danceGrooveSource(prompt, nil)
			phrase := "dance groove"
			if strings.Contains(prompt, "celebratory dance groove") {
				phrase = "celebratory dance groove"
			}
			if strings.Contains(prompt, "dance grooves") {
				phrase += "s"
			}
			found := false
			for _, atom := range source.Atoms {
				if atom.Kind == "genre" && atom.Value == "dance" {
					t.Fatalf("provider lookup reintroduced a defining genre: %+v", atom)
				}
				if atom.Kind != "texture" || atom.Value != phrase {
					continue
				}
				found = true
				if atom.ConceptID != "" || atom.Strength != "essential" || atom.Polarity != "positive" || len(atom.Evidence) != 1 {
					t.Fatalf("literal groove acquired unsupported meaning: %+v", atom)
				}
				e := atom.Evidence[0]
				if e.Start != strings.Index(prompt, phrase) || e.End != e.Start+len(phrase) || e.Text != phrase {
					t.Fatalf("source byte offsets changed: %+v", e)
				}
			}
			if !found {
				t.Fatalf("literal groove missing: %+v", source.Atoms)
			}
			for _, kind := range []string{"rules", "empty wire", "literal texture wire", "genre wire"} {
				t.Run(kind, func(t *testing.T) {
					var intent core.MusicIntent
					var err error
					if kind == "rules" {
						intent, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
					} else {
						wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10}
						if kind == "literal texture wire" {
							wire.Textures = []schema.WirePreference{{Value: phrase, Span: phrase, Influence: "positive", Explicit: true}}
						}
						if kind == "genre wire" {
							wire.Genres = []schema.WirePreference{{Value: "dance", Span: "dance", Influence: "positive", Explicit: true}}
						}
						raw, _ := json.Marshal(wire)
						intent, err = schema.ParseForPromptWithSource(raw, prompt, source)
					}
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, clause := range audio.Clauses(intent) {
						if clause.Kind == "genre" && clause.Text == "dance" {
							t.Fatalf("groove became a genre eligibility gate: %+v", clause)
						}
						if clause.Kind == "texture" && clause.Text == phrase {
							found = true
							if !clause.Essential || clause.Strict || clause.Negative {
								t.Fatalf("defining groove request lost force: %+v", clause)
							}
						}
					}
					if !found {
						t.Fatalf("groove missing from audio clauses: %+v", intent.Preferences)
					}
				})
			}
		})
	}
}

func TestDanceGrooveRetainsSourcePolarityAndStrength(t *testing.T) {
	for _, test := range []struct{ prompt, polarity, strength string }{
		{"10 songs without dance grooves", "negative", "required"},
		{"10 songs with only dance grooves", "positive", "required"},
	} {
		source := danceGrooveSource(test.prompt, nil)
		found := false
		for _, atom := range source.Atoms {
			if atom.Kind == "genre" && atom.Value == "dance" {
				t.Fatalf("genre leaked into scoped groove: %+v", atom)
			}
			if atom.Kind == "texture" && atom.Value == "dance grooves" {
				found = true
				if atom.Polarity != test.polarity || atom.Strength != test.strength {
					t.Fatalf("source role changed: %+v", atom)
				}
			}
		}
		if !found {
			t.Fatalf("scoped groove missing: %+v", source.Atoms)
		}
	}
}

func TestDanceGenreRemainsDefiningWhenExplicit(t *testing.T) {
	for _, prompt := range []string{"10 tracks of dance music", "10 dance tracks", `10 songs in the genre "dance"`} {
		source := danceGrooveSource(prompt, nil)
		wire, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10})
		intent, err := schema.ParseForPromptWithSource(wire, prompt, source)
		if err != nil {
			t.Fatal(err)
		}
		if len(intent.EssentialCriteria) != 1 || intent.EssentialCriteria[0].Kind != "genre" || intent.EssentialCriteria[0].Value != "dance" {
			t.Fatalf("explicit Dance category lost: %+v", intent.EssentialCriteria)
		}
	}
}

func TestDanceGrooveReferenceSpansRemainProtected(t *testing.T) {
	for _, prompt := range []string{`10 songs like "Celebratory Dance Groove" by Low`, `10 songs by the artist "Dance Groove"`, "10 songs by the artist Dance Groove"} {
		source := danceGrooveSource(prompt, danceGrooveIdentityLookup{})
		entity := false
		grounded := false
		for _, atom := range source.Atoms {
			if atom.Kind == "genre" || atom.Kind == "texture" {
				t.Fatalf("reference became a musical request: %+v", atom)
			}
			if atom.Kind == "track" || atom.Kind == "artist" {
				entity = true
				if atom.Kind == "artist" && atom.Grounding != nil && len(atom.Grounding.Candidates) == 1 && atom.Grounding.Candidates[0].ID == "11111111-2222-3333-4444-555555555555" {
					grounded = true
				}
			}
		}
		if !entity {
			t.Fatalf("reference lost: %+v", source.Atoms)
		}
		if strings.Contains(prompt, "the artist") && !grounded {
			t.Fatalf("independent identity grounding lost: %+v", source.Atoms)
		}
	}
}

func danceGrooveSource(prompt string, lookup recognition.IdentityLookup) core.IntentTranslation {
	vocabulary := &genrevocab.Vocabulary{Genres: []genrevocab.Genre{{Name: "dance"}}}
	return recognition.Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, vocabulary)
}

type danceGrooveIdentityLookup struct{}

func (danceGrooveIdentityLookup) SnapshotIdentity() mbindex.SnapshotIdentity {
	return mbindex.SnapshotIdentity{IndexVersion: mbindex.IndexVersion, Snapshot: "synthetic-dance-groove"}
}

func (danceGrooveIdentityLookup) LookupArtistNames(_ context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	var matches []mbindex.ArtistNameLookup
	for _, name := range names {
		if key := core.NormalizeIdentityPart(name); key == "dance groove" {
			matches = append(matches, mbindex.ArtistNameLookup{Name: name, NameKey: key, Candidates: []mbindex.ArtistIdentity{{MBID: "11111111-2222-3333-4444-555555555555", Name: "Dance Groove", MatchType: mbindex.ArtistMatchCanonical}}})
		}
	}
	return matches, nil
}

func (danceGrooveIdentityLookup) LookupArtistRecordings(context.Context, []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	return nil, nil
}
