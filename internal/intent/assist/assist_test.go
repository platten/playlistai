package assist

import (
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

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

func TestReviewedExtractorMessageRetainsSourceAuthority(t *testing.T) {
	proposals := []core.IntentProposal{{Origin: "distilbert", Kind: "artist", Role: "similarity", Source: core.SourceEvidence{Text: "Aerosmith"}, Advisory: true}}
	message := Message(proposals)
	if !strings.Contains(message, "NOT protected facts") || !strings.Contains(message, "Source-span interpretation proposal") || !strings.Contains(message, "Aerosmith") {
		t.Fatal(message)
	}
	if Message(nil) != "" {
		t.Fatal("empty proposals added prompt text")
	}
}
