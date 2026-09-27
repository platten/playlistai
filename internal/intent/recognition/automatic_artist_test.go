package recognition

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/mbindex"
)

type automaticLookup struct{ IdentityLookup }

func (automaticLookup) AutomaticArtistResolution() bool { return true }
func listenerCount(n int64) *core.ArtistPopularity {
	return &core.ArtistPopularity{Snapshot: "fixture-pop", UniqueListeners: &n}
}

func TestAutomaticArtistKeepsAliasesAlternativesAndExplicitContext(t *testing.T) {
	for _, tc := range []struct {
		prompt, name string
		artists      []mbindex.ArtistIdentity
		want         string
	}{
		{"like Fela", "fela", []mbindex.ArtistIdentity{{MBID: "obscure", Name: "Fela", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(1)}, {MBID: "kuti", Name: "Fela Kuti", MatchType: mbindex.ArtistMatchAlias, Popularity: listenerCount(100)}}, "kuti"},
		{"like John Williams (classical guitarist)", "john williams", []mbindex.ArtistIdentity{{MBID: "film", Name: "John Williams", Disambiguation: "film composer", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(100)}, {MBID: "guitar", Name: "John Williams", Disambiguation: "Australian classical guitarist", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(1)}}, "guitar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := automaticLookup{referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{tc.name: tc.artists}}}
			source := Apply(context.Background(), tc.prompt, lexicon.Extract(tc.prompt), lookup, nil)
			atoms := groundedAtoms(source)
			if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != 2 || atoms[0].Grounding.Confirmed {
				t.Fatalf("choices lost: %+v", atoms)
			}
			if selected, ok := atoms[0].Grounding.DecidedArtist(); !ok || selected.ID != tc.want {
				t.Fatalf("wrong default: %+v", atoms[0].Grounding)
			}
		})
	}
	lookup := automaticLookup{radioheadRecognitionFixture()}
	const prompt = "like Radiohead"
	atoms := groundedAtoms(Apply(context.Background(), prompt, lexicon.Extract(prompt), Combine(lookup), nil))
	if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != 2 || atoms[0].Grounding.Decision.SelectedID != "a74b1b7f-71a5-4011-9441-d0b5e4122711" {
		t.Fatalf("Radiohead default: %+v", atoms)
	}
	const recording = "like Radiohead — Early Demo"
	atoms = groundedAtoms(Apply(context.Background(), recording, lexicon.Extract(recording), automaticLookup{predecessorRecordingLookup{radioheadRecognitionFixture()}}, nil))
	if len(atoms) != 1 || atoms[0].Kind != "track" || atoms[0].Grounding.Candidates[0].ID != "early-recording" {
		t.Fatal("popularity displaced explicit predecessor recording")
	}
}
