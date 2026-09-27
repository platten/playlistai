package recognition

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/mbindex"
)

func TestCombinedSentencePeriodKeepsExactIdentityAndFullEvidence(t *testing.T) {
	const daft = "056e4f3e-d505-4dad-8ec1-d04f521cbb56"
	for _, tc := range []struct {
		name, prompt, spelling, packName, want string
		identities                             map[string][]mbindex.ArtistIdentity
		count                                  int
	}{
		{"sentence period", "Give me 10 tracks like Daft Punk.", "Daft Punk", "Daft Punk", daft, map[string][]mbindex.ArtistIdentity{"daft punk": {{MBID: daft, Name: "Daft Punk", MatchType: mbindex.ArtistMatchCanonical}}}, 1},
		{"popular alias retains namesake", "Give me tracks like Fela.", "Fela", "Fela", "kuti", map[string][]mbindex.ArtistIdentity{"fela": {{MBID: "namesake", Name: "Fela", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(1)}, {MBID: "kuti", Name: "Fela Kuti", MatchType: mbindex.ArtistMatchAlias, Popularity: listenerCount(100)}}}, 2},
		{"explicit context", "Like John Williams. (classical guitarist)", "John Williams", "John Williams", "guitar", map[string][]mbindex.ArtistIdentity{"john williams": {{MBID: "film", Name: "John Williams", Disambiguation: "film composer", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(100)}, {MBID: "guitar", Name: "John Williams", Disambiguation: "classical guitarist", MatchType: mbindex.ArtistMatchCanonical, Popularity: listenerCount(1)}}}, 2},
		{"real punctuation keeps exact identity", "Like M.I.A.", "M.I.A.", "M.I.A.", "mia-full", map[string][]mbindex.ArtistIdentity{"m.i.a.": {{MBID: "mia-full", Name: "M.I.A.", MatchType: mbindex.ArtistMatchCanonical}}, "m.i.a": {{MBID: "mia-other", Name: "M.I.A", MatchType: mbindex.ArtistMatchCanonical}}}, 1},
		{"punctuated pack name stays distinct", "Like M.I.A.", "M.I.A.", "M.I.A.", "paipack-artist:m.i.a", map[string][]mbindex.ArtistIdentity{"m.i.a": {{MBID: "mia-other", Name: "M.I.A", MatchType: mbindex.ArtistMatchCanonical}}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pack := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{}}
			for _, spelling := range []string{tc.packName, strings.TrimRight(tc.packName, "."), strings.TrimRight(tc.packName, ".") + "."} {
				pack.names[core.NormalizeIdentityPart(spelling)] = []mbindex.ArtistIdentity{{MBID: "paipack-artist:" + core.NormalizeIdentityPart(strings.TrimRight(tc.packName, ".")), Name: tc.packName, MatchedName: spelling, MatchType: mbindex.ArtistMatchCanonical}}
			}
			lookup := Combine(automaticLookup{referenceContextLookup{names: tc.identities}}, pack)
			before, _ := json.Marshal(tc.identities)
			source := Apply(context.Background(), tc.prompt, lexicon.Extract(tc.prompt), lookup, nil)
			atoms := groundedAtoms(source)
			if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != tc.count {
				t.Fatalf("wrong candidates: %+v", atoms)
			}
			g := atoms[0].Grounding
			chosen, ok := g.DecidedArtist()
			if !ok || chosen.ID != tc.want || g.MatchedSpelling != tc.spelling {
				t.Fatalf("wrong identity/spelling: %+v decision=%+v", g, chosen)
			}
			wantText := strings.TrimRight(tc.spelling, ".") + "."
			e := atoms[0].Evidence[0]
			if e.Text != wantText || e.Text != tc.prompt[e.Start:e.End] {
				t.Fatalf("source punctuation/span lost: %+v", e)
			}
			after, _ := json.Marshal(tc.identities)
			if string(before) != string(after) {
				t.Fatal("source provider identities mutated")
			}
			clone := source.Clone()
			for i := range clone.Atoms {
				if clone.Atoms[i].Grounding != nil {
					clone.Atoms[i].Grounding.Candidates[0].Name = "changed clone"
					break
				}
			}
			if reflect.DeepEqual(clone, source) || groundedAtoms(source)[0].Grounding == nil {
				t.Fatal("saved grounding shared with clone")
			}
		})
	}
}

func TestSentencePeriodMatchPreservesTruncationAndSourceOccurrences(t *testing.T) {
	const id = "056e4f3e-d505-4dad-8ec1-d04f521cbb56"
	lookup := func(truncated bool) IdentityLookup {
		return Combine(automaticLookup{referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{"daft punk": {{MBID: id, Name: "Daft Punk", MatchType: mbindex.ArtistMatchCanonical}}}, truncated: truncated}}, referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{"daft punk.": {{MBID: "paipack-artist:daft punk", Name: "Daft Punk", MatchType: mbindex.ArtistMatchCanonical}}}})
	}
	const prompt = "Avoid Daft Punk. Include Daft Punk."
	atoms := groundedAtoms(Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup(false), nil))
	if len(atoms) != 2 || atoms[0].Polarity != "negative" || atoms[1].Polarity != "positive" {
		t.Fatalf("occurrence roles merged: %+v", atoms)
	}
	for _, atom := range atoms {
		if atom.Grounding.Candidates[0].ID != id || atom.Evidence[0].Text != "Daft Punk." {
			t.Fatalf("occurrence identity or evidence lost: %+v", atom)
		}
	}
	atoms = groundedAtoms(Apply(context.Background(), "Like Daft Punk.", lexicon.Extract("Like Daft Punk."), lookup(true), nil))
	if len(atoms) != 1 || !atoms[0].Grounding.Truncated || atoms[0].Grounding.Decision != nil {
		t.Fatalf("incomplete lookup became an automatic decision: %+v", atoms)
	}
}
