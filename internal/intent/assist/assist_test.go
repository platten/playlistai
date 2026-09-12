package assist

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

type embedFunc func(context.Context, string) ([]float32, error)

func (f embedFunc) EmbedText(ctx context.Context, s string) ([]float32, error) { return f(ctx, s) }

func TestHardOperatorsAndKnownEntitiesNeverBecomeSimilarityFacts(t *testing.T) {
	m := Mapper{Embedder: embedFunc(func(context.Context, string) ([]float32, error) {
		t.Fatal("model should not run on protected/operator phrases")
		return nil, nil
	})}
	for _, prompt := range []string{"no glittery cosmic haze", "like Aerosmith", "only frothy featherlike sounds", "start with vaporous crystalline layers", "10 tracks", "quiet classical"} {
		got, err := m.Propose(context.Background(), prompt)
		if err != nil || len(got) != 0 {
			t.Fatalf("%q: %v %v", prompt, got, err)
		}
	}
}

func TestAdvisoryCannotBecomeHardConstraintAndDoesNotEraseAnotherOccurrence(t *testing.T) {
	source := core.SourceEvidence{Text: "floating clouds", Start: 20, End: 35}
	explicit := core.SourceEvidence{Text: "relaxed", Start: 0, End: 7, Explicit: true}
	m := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "mood", Value: "relaxed", Evidence: []core.SourceEvidence{source}}, {Kind: "mood", Value: "relaxed", Evidence: []core.SourceEvidence{explicit}}}, HardConstraints: []core.HardConstraint{{Kind: "require_mood", Value: "relaxed", Evidence: []core.SourceEvidence{source}}}}
	m.Preferences.Moods = []core.IntentPreference{{Value: "relaxed", Strength: "required", Explicit: true, Evidence: []core.SourceEvidence{source}}}
	m = KeepAdvisory(m, []core.IntentProposal{{Value: "relaxed", Source: source, Advisory: true}})
	if len(m.HardConstraints) != 0 || len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Evidence[0].Start != 0 {
		t.Fatalf("hard override or explicit occurrence lost: %+v", m)
	}
	if m.Preferences.Moods[0].Strength != "preferred" || m.Preferences.Moods[0].Explicit {
		t.Fatal("advisory became required")
	}
}

func TestAdvisoryPreservesDistinctOccurrencesAndMixedEvidence(t *testing.T) {
	p := core.IntentProposal{Value: "relaxed", Source: core.SourceEvidence{Text: "floating", Start: 30, End: 38}, Advisory: true}
	other := core.SourceEvidence{Text: "floating", Start: 0, End: 8, Explicit: true}
	m := core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "require_mood", Value: "relaxed", Evidence: []core.SourceEvidence{other}}, {Kind: "require_mood", Value: "relaxed", Evidence: []core.SourceEvidence{p.Source, other}}}}
	m = KeepAdvisory(m, []core.IntentProposal{p})
	if len(m.HardConstraints) != 2 {
		t.Fatal("independent source facts lost", m.HardConstraints)
	}
}

func TestDictionaryMappingIsAnAdvisoryWithOriginalSpan(t *testing.T) {
	v := make([]float32, 384)
	v[0] = 1
	other := make([]float32, 384)
	other[1] = 1
	m := Mapper{Embedder: embedFunc(func(context.Context, string) ([]float32, error) { return v, nil }), Identity: "test-model", vectors: [][]float32{v, other}, concepts: []musicconcepts.Concept{{ID: "mood:relaxed", Kind: "mood", Value: "relaxed"}, {ID: "mood:angry", Kind: "mood", Value: "angry"}}}
	prompt := "featherlight clouds floating"
	got, err := m.Propose(context.Background(), prompt)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	p := got[0]
	if !p.Advisory || p.Source.Text != prompt || p.Source.Start != 0 || p.Source.End != len(prompt) || p.Model != "test-model" {
		t.Fatal(p)
	}
	if !strings.Contains(Message(got), "NOT protected facts") {
		t.Fatal("missing advisory boundary")
	}
}

func TestCancelledMappingAndMalformedVectors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := Mapper{Embedder: embedFunc(func(context.Context, string) ([]float32, error) { return make([]float32, 384), nil })}
	if _, err := m.Propose(ctx, "featherlight clouds floating"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.Propose(context.Background(), "featherlight clouds floating"); err == nil {
		t.Fatal("zero embedding accepted")
	}
}
