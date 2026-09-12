package main

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestGenreJourneyChecksDoNotCountScopedVocalCriteriaAsGenres(t *testing.T) {
	m := core.MusicIntent{Mode: core.ModeJourney, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "journey_start"}, {Kind: "genre", Value: "industrial rock", Scope: "journey_end"}, {Kind: "vocal", Value: "instrumental", Scope: "journey_start"}}}
	c := promptCase{JourneyGenres: []string{"electronic", "industrial rock"}}
	if issues := checkIntent(c, m); len(issues) > 0 {
		t.Fatal(issues)
	}
	m.EssentialCriteria = m.EssentialCriteria[1:]
	if len(checkIntent(c, m)) == 0 {
		t.Fatal("missing genre accepted because a vocal stage remained")
	}
}

func TestGenreExclusionRecognizesConsumerRepresentationButRequiresValue(t *testing.T) {
	want := &meaningExpectation{Constraints: []constraintExpectation{{Kind: "exclude_genre", Value: "rock"}}}
	m := core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}}
	if issues := checkMeaning(want, m); len(issues) > 0 {
		t.Fatal(issues)
	}
	for _, c := range []core.HardConstraint{{Kind: "exclude_mood", Value: "rock"}, {Kind: "exclude_style", Value: "metal"}, {Kind: "require_style", Value: "rock"}} {
		m.HardConstraints = []core.HardConstraint{c}
		if len(checkMeaning(want, m)) == 0 {
			t.Fatal("wrong exclusion accepted", c)
		}
	}
}
