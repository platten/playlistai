package lexicon

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestReadableFactsKeepRolesAndHideProviderIdentifiers(t *testing.T) {
	source := Extract("Avoid Air; mostly instrumental or vocals")
	a := &source.Atoms[0]
	a.Kind, a.Value, a.Scope, a.Polarity, a.Strength, a.Degree, a.Group = "exclude_artist", "Air", "journey_end", "negative", "required", "plain", "choice-1"
	a.Grounding = &core.IdentityGrounding{MatchType: "alias", SnapshotVersion: "secret-snapshot", Candidates: []core.IdentityCandidate{{ID: "secret-id", Name: "Air"}, {ID: "secret-id-2", Name: "Air"}}, Truncated: true}
	got := FactsMessage(source)
	for _, want := range []string{"polarity=negative", "scope=journey_end", "strength=required", `degree="plain"`, `alternative-group="choice-1"`, `canonical="Air"`, `match="alias"`, "multiple candidates, truncated results", "never choose an ambiguous identity"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, atom := range source.Atoms {
		for _, e := range atom.Evidence {
			if !strings.Contains(got, fmt.Sprintf("source=%q", e.Text)) {
				t.Fatal("lost occurrence", e)
			}
		}
	}
	if strings.Contains(got, "secret-") {
		t.Fatal("opaque identity in prose", got)
	}
	a.Grounding.Candidates = a.Grounding.Candidates[:1]
	a.Grounding.Truncated, a.Grounding.Confirmed = false, true
	if got = FactsMessage(source); !strings.Contains(got, "one candidate, user-confirmed identity") {
		t.Fatal(got)
	}
}

func TestParsingContextPreparationBoundsPriorityAndIsolation(t *testing.T) {
	source := Extract("dark mood with warm timbre, instrumental music, bright timbre, romantic mood, dynamic range and compressed dynamics; like Air")
	a := core.IntentAtom{ID: "artist", Kind: "artist", Value: "Air", Evidence: []core.SourceEvidence{{Text: "Air", Start: 200, End: 203}}, Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{ID: "a", Name: "Air", Disambiguation: "French duo"}, {ID: "b", Name: "Air", Disambiguation: "Jazz trio"}, {ID: "c", Name: "Air", Disambiguation: "Third artist"}, {ID: "d", Name: "Fourth should be omitted"}}}}
	source.Atoms = append(source.Atoms, a)
	before, _ := json.Marshal(source)
	input := ports.IntentInput{SourceFacts: &source, EnrichParsingContext: true, RecognitionIdentity: "snapshot"}
	first := PrepareParsingContext(input)
	second := PrepareParsingContext(input)
	if !reflect.DeepEqual(first.SourceFacts, second.SourceFacts) {
		t.Fatal("nondeterministic context")
	}
	evidence := first.SourceFacts.ParsingContext
	if len(evidence.Hints) == 0 || evidence.Hints[0].Kind != "identity" || !strings.Contains(evidence.Hints[0].Text, "other candidates omitted") || strings.Contains(evidence.Hints[0].Text, "Fourth should") {
		t.Fatalf("priority/candidate cap: %+v", evidence)
	}
	text := ContextHeader
	for _, h := range evidence.Hints {
		text += h.Text
		if h.AtomID == "" || !utf8.ValidString(h.Text) || !strings.HasSuffix(h.Text, "\n") {
			t.Fatal("incomplete record", h)
		}
	}
	if len(text) > MaxContextBytes || len(evidence.Hints) > MaxContextRecords || evidence.OmittedRecords == 0 {
		t.Fatalf("unbounded context: %d %+v", len(text), evidence)
	}
	again := PrepareParsingContext(first)
	if again.SourceFacts != first.SourceFacts || again.RecognitionIdentity != first.RecognitionIdentity {
		t.Fatal("prepared snapshot not reused")
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("supplied facts mutated")
	}
	baseline := PrepareParsingContext(ports.IntentInput{SourceFacts: &source, RecognitionIdentity: "snapshot"})
	if baseline.SourceFacts.ParsingContext != nil || baseline.RecognitionIdentity == first.RecognitionIdentity {
		t.Fatal("baseline changed or cache collision")
	}
	changed := source.Clone()
	changed.Atoms[len(changed.Atoms)-1].Grounding.Candidates[0].Disambiguation = "different reference data"
	next := PrepareParsingContext(ports.IntentInput{SourceFacts: &changed, EnrichParsingContext: true, RecognitionIdentity: "snapshot"})
	if next.RecognitionIdentity == first.RecognitionIdentity {
		t.Fatal("changed content reused cache identity")
	}
}

func TestContextKeepsOversizeNamesWholeAndLabelsRelationships(t *testing.T) {
	source := Extract("warm timbre, dark mood, industrial rock and unfamiliar moon velvet")
	source.Atoms = append(source.Atoms, core.IntentAtom{ID: "large", Evidence: []core.SourceEvidence{{Text: "完整名字", Start: 0}}, Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Name: strings.Repeat("名字", 1024)}}}})
	got := PrepareParsingContext(ports.IntentInput{SourceFacts: &source, EnrichParsingContext: true}).SourceFacts.ParsingContext
	all := ""
	for _, h := range got.Hints {
		if h.AtomID == "large" {
			t.Fatal("oversized record admitted")
		}
		all += h.Text
	}
	if !strings.Contains(all, "meaning=") || strings.Contains(all, "unfamiliar moon velvet") || !strings.Contains(all, "not synonyms or requirements") {
		t.Fatal(all)
	}
	if source.OriginalText != "warm timbre, dark mood, industrial rock and unfamiliar moon velvet" {
		t.Fatal("unknown text changed")
	}
}
