package lexicon_test

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestComposedByIsComposerNotPerformer(t *testing.T) {
	prompt := "Relaxing Classical music composed only by Fryderyk Chopin"
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.References) != 0 || len(intent.RequiredTracks) != 0 {
		t.Fatalf("composer became a performer or required recording: %+v", intent)
	}
	if len(intent.EssentialCriteria) != 2 {
		t.Fatalf("genre or composer lost: %+v", intent.EssentialCriteria)
	}
	composer := intent.EssentialCriteria[1]
	if composer.Kind != "composer" || composer.Value != "Fryderyk Chopin" || composer.Scope != "playlist" || composer.Strength != "required" || len(composer.Evidence) != 1 || composer.Evidence[0].Text != "Fryderyk Chopin" {
		t.Fatalf("composer field not preserved: %+v", composer)
	}
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind == "require_artist" {
			t.Fatalf("composer became performer-only policy: %+v", constraint)
		}
	}
}

func TestComposerSourceOverridesModelPerformerGuess(t *testing.T) {
	prompt := "music composed by Fryderyk Chopin"
	source := lexicon.Extract(prompt)
	guessed := core.MusicIntent{OriginalDescription: prompt,
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Fryderyk Chopin", Influence: core.InfluencePositive,
			Evidence: []core.SourceEvidence{{Text: "Fryderyk Chopin", Start: 18, End: len(prompt), Explicit: true}}}},
		HardConstraints: []core.HardConstraint{{Kind: "require_artist", Value: "Fryderyk Chopin"}},
	}
	got := lexicon.Reconcile(guessed, source)
	if len(got.References) != 0 || len(got.HardConstraints) != 0 || len(got.EssentialCriteria) != 1 || got.EssentialCriteria[0].Kind != "composer" {
		t.Fatalf("model performer guess overrode source composer: %+v", got)
	}
}

func TestComposerOnlyWordingKeepsNameWithoutQualifier(t *testing.T) {
	for _, prompt := range []string{
		"Classical music only composed by Fryderyk Chopin",
		"Classical music composed by Fryderyk Chopin only",
	} {
		source := lexicon.Extract(prompt)
		found := false
		for _, atom := range source.Atoms {
			found = found || atom.Kind == "composer" && atom.Value == "Fryderyk Chopin" && atom.Strength == "required"
		}
		if !found {
			t.Fatalf("composer name absorbed an only qualifier: %q %+v", prompt, source.Atoms)
		}
	}
}
