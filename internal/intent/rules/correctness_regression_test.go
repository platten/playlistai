package rules

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestCategoryRemainsEssentialAlongsideExplicitReference(t *testing.T) {
	for _, prompt := range []string{
		"electronic music like Seed Artist", "electronic tracks like Seed Artist",
		"electronic songs like Seed Artist", "electronic playlist like Seed Artist",
		"play electronic music by Seed Artist",
	} {
		t.Run(prompt, func(t *testing.T) {
			intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if len(intent.References) != 1 || intent.References[0].Query != "Seed Artist" {
				t.Fatalf("explicit reference lost: %+v", intent.References)
			}
			if len(intent.EssentialCriteria) != 1 || intent.EssentialCriteria[0].Value != "electronic" || intent.EssentialCriteria[0].Scope != "playlist" {
				t.Fatalf("essential category lost beside reference: %+v", intent.EssentialCriteria)
			}
		})
	}
}

func TestArtistNamesDoNotBecomeMusicalEvidence(t *testing.T) {
	for prompt, want := range map[string]string{
		"play Aesop Rock": "Aesop Rock", "play music by Electronic": "Electronic",
		"like Aesop Rock": "Aesop Rock", "music by Jazz Sabbath": "Jazz Sabbath",
	} {
		t.Run(prompt, func(t *testing.T) {
			intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if len(intent.References) != 1 || intent.References[0].Query != want {
				t.Fatalf("explicit artist lost: %+v", intent.References)
			}
			if len(intent.EssentialCriteria) != 0 || len(intent.Preferences.Styles) != 0 {
				t.Fatalf("artist name became genre evidence: %+v", intent)
			}
		})
	}
}

func TestCategoryAndSameNamedArtistKeepSeparateSourceSpans(t *testing.T) {
	prompt := "electronic music like Electronic"
	intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
	if len(intent.References) != 1 || len(intent.EssentialCriteria) != 1 {
		t.Fatalf("lost category or explicit artist: %+v", intent)
	}
	if intent.EssentialCriteria[0].Evidence[0].Start != 0 || intent.References[0].Evidence[0].Start != len("electronic music like ") {
		t.Fatalf("category and artist source spans conflated: %+v", intent)
	}
}

func TestCategoryExclusionDoesNotBecomeBareArtistOrBroaderGenre(t *testing.T) {
	for prompt, want := range map[string]string{
		"electronic music, no rock":          "rock",
		"electronic music, no rock & roll":   "rock & roll",
		"electronic music, no rock and roll": "rock & roll",
		"rock music, no electronic":          "electronic",
	} {
		t.Run(prompt, func(t *testing.T) {
			intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if len(intent.References) != 0 || len(intent.EssentialCriteria) != 1 {
				t.Fatalf("category misclassified: %+v", intent)
			}
			var exclusions []string
			for _, constraint := range intent.HardConstraints {
				if constraint.Kind == "exclude_style" {
					exclusions = append(exclusions, constraint.Value)
				}
			}
			if len(exclusions) != 1 || exclusions[0] != want {
				t.Fatalf("exclusion scope changed: %v", exclusions)
			}
			if len(intent.Preferences.Styles) != 2 || intent.Preferences.Styles[1].Influence != core.InfluenceNegative {
				t.Fatalf("negative genre lost: %+v", intent.Preferences.Styles)
			}
		})
	}
}

func TestElectronicaAliasesAreCategories(t *testing.T) {
	for prompt, want := range map[string]string{
		"electronica": "electronic", "electronica music": "electronic",
		"ambient electronica": "ambient electronic", "ambient electronica music": "ambient electronic",
	} {
		t.Run(prompt, func(t *testing.T) {
			intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			if len(intent.References) != 0 {
				t.Fatalf("electronica category became a seed: %+v", intent.References)
			}
			if len(intent.EssentialCriteria) != 1 || intent.EssentialCriteria[0].Value != want {
				t.Fatalf("alias was not canonicalized: %+v", intent.EssentialCriteria)
			}
		})
	}
	intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: "music by Electronica"})
	if len(intent.References) != 1 || intent.References[0].Query != "Electronica" || len(intent.EssentialCriteria) != 0 {
		t.Fatalf("explicit same-named artist context was lost: %+v", intent)
	}
}
