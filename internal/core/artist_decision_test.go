package core

import (
	"encoding/json"
	"testing"
)

func audience(users, listens int64) *ArtistPopularity {
	return &ArtistPopularity{Snapshot: "audience-v1", UniqueListeners: &users, Listens: &listens}
}

func artistDefaults(name string) *IdentityGrounding {
	return &IdentityGrounding{Provider: "MusicBrainz", MatchedSpelling: name, SnapshotVersion: "names-v1", Candidates: []IdentityCandidate{
		{Kind: ReferenceArtist, ID: "a", Name: name, MatchType: "canonical", Popularity: audience(100, 1000)},
		{Kind: ReferenceArtist, ID: "b", Name: name, MatchType: "canonical", Popularity: audience(10, 9000)},
	}}
}

func TestArtistDecisionPopularityContextAndUnknowns(t *testing.T) {
	for _, tc := range []struct {
		name             string
		change           func(*IdentityGrounding)
		selected, method string
		provisional      bool
	}{
		{"Nick Drake", nil, "a", "popularity", false},
		{"Fela", func(g *IdentityGrounding) { g.Candidates[0].Name = "Fela Kuti"; g.Candidates[0].MatchType = "alias" }, "a", "popularity", false},
		{"John Williams", func(g *IdentityGrounding) {
			g.Candidates[0].Disambiguation = "film composer"
			g.Candidates[1].Disambiguation = "Australian classical guitarist"
			g.Decision = ArtistContextDecision(g, "classical guitarist")
		}, "b", "context", false},
		{"Shared Name", func(g *IdentityGrounding) { g.Candidates[1].Popularity = nil }, "a", "popularity", true},
		{"listeners tie", func(g *IdentityGrounding) { g.Candidates[1].Popularity = audience(100, 1001) }, "b", "popularity", false},
		{"true tie", func(g *IdentityGrounding) { g.Candidates[1].Popularity = audience(100, 1000) }, "", "", false},
		{"unknown tie breaker", func(g *IdentityGrounding) {
			g.Candidates[1].Popularity = audience(100, 1001)
			g.Candidates[1].Popularity.Listens = nil
		}, "", "", false},
		{"all unknown", func(g *IdentityGrounding) { g.Candidates[0].Popularity = nil; g.Candidates[1].Popularity = nil }, "", "", false},
		{"incomplete", func(g *IdentityGrounding) { g.Truncated = true }, "", "", false},
		{"explicit choice", func(g *IdentityGrounding) { g.Confirmed = true }, "", "", false},
		{"different snapshot", func(g *IdentityGrounding) { g.Candidates[1].Popularity.Snapshot = "other" }, "", "", false},
		{"credit only", func(g *IdentityGrounding) { g.Candidates[0].MatchType = "credit" }, "b", "popularity", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := artistDefaults(tc.name)
			if tc.change != nil {
				tc.change(g)
			}
			for range 2 {
				d := DecideArtist(g)
				if tc.selected == "" {
					if d != nil {
						t.Fatalf("unexpected choice: %+v", d)
					}
				} else {
					if d == nil || d.SelectedID != tc.selected || d.Method != tc.method || d.Provisional != tc.provisional {
						t.Fatalf("choice: %+v", d)
					}
					g.Decision = d
					if c, ok := g.DecidedArtist(); !ok || c.ID != tc.selected {
						t.Fatalf("invalid saved choice: %+v %v", c, ok)
					}
				}
				g.Candidates[0], g.Candidates[1] = g.Candidates[1], g.Candidates[0]
			}
		})
	}
}

func TestArtistDecisionCanonicalFallbackAndCoperformancePrecedence(t *testing.T) {
	g := artistDefaults("Radiohead")
	g.Candidates[0].Popularity = nil
	g.Candidates[1].Popularity = nil
	g.Candidates[1].Name = "On a Friday"
	g.Candidates[1].MatchType = "alias"
	if d := DecideArtist(g); d == nil || d.SelectedID != "a" || d.Method != "canonical" {
		t.Fatalf("canonical fallback: %+v", d)
	}
	g = identityProofFixture()
	g.MatchedSpelling = "Tony Allen"
	for i := range g.Candidates {
		g.Candidates[i].MatchType = "canonical"
		g.Candidates[i].Popularity = audience(int64(100+i*100), 1000)
	}
	g.Decision = &ArtistDecision{SelectedID: corroborationArtistB, Method: "popularity", PolicyVersion: ArtistDecisionPolicy, Snapshot: "audience-v1"}
	if d := DecideArtist(g); d != nil {
		t.Fatal("popularity replaced collaborator evidence")
	}
	if c, ok := g.DecidedArtist(); !ok || c.ID != corroborationArtistA {
		t.Fatal("Fela context did not win")
	}
}

func TestArtistDecisionCloningAndHistoryRoundTrip(t *testing.T) {
	g := artistDefaults("Nick Drake")
	g.Decision = DecideArtist(g)
	m := MusicIntent{Version: CurrentIntentVersion, PreparedMusicSnapshot: "prepared-v1", References: []IntentReference{{Kind: ReferenceArtist, Query: "Nick Drake", Grounding: g}}, Translation: &IntentTranslation{Atoms: []IntentAtom{{Kind: "artist", Value: "Nick Drake", Grounding: g}}}}
	clone := m.Normalized()
	clone.References[0].Grounding.Decision.SelectedID = "changed"
	*clone.References[0].Grounding.Candidates[0].Popularity.UniqueListeners = 999
	clone.Translation.Atoms[0].Grounding.Candidates[0].Popularity.Snapshot = "changed"
	if g.Decision.SelectedID != "a" || *g.Candidates[0].Popularity.UniqueListeners != 100 || g.Candidates[0].Popularity.Snapshot != "audience-v1" {
		t.Fatal("clone mutated source")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored MusicIntent
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if c, ok := restored.Normalized().References[0].Grounding.DecidedArtist(); !ok || c.ID != "a" {
		t.Fatal("saved choice lost")
	}
	if clone.PreparedMusicSnapshot != "prepared-v1" || restored.Normalized().PreparedMusicSnapshot != "prepared-v1" {
		t.Fatal("prepared music snapshot pin lost")
	}
	g.Decision.SelectedID = "b"
	if _, ok := g.DecidedArtist(); ok {
		t.Fatal("forged winner accepted")
	}
	if d := ArtistContextDecision(artistDefaults("John Williams"), "not the composer"); d != nil {
		t.Fatal("negative context used as positive identity")
	}
}

func TestArtistCorrectionRetainsChoicesAndExplicitPrecedence(t *testing.T) {
	g := artistDefaults("Nick Drake")
	g.Decision = DecideArtist(g)
	chosen, ok := g.WithConfirmedIdentity(ReferenceArtist, "b")
	if !ok || !chosen.Confirmed || chosen.Decision != nil || len(chosen.Candidates) != 1 || chosen.Candidates[0].ID != "b" || len(chosen.IdentityChoices()) != 2 {
		t.Fatalf("correction: %+v", chosen)
	}
	if DecideArtist(chosen) != nil {
		t.Fatal("popularity overrode user correction")
	}
	back, ok := chosen.WithConfirmedIdentity(ReferenceArtist, "a")
	if !ok || back.Candidates[0].ID != "a" || len(back.Alternatives) != 1 {
		t.Fatal("second correction lost alternatives")
	}
	*back.Alternatives[0].Popularity.UniqueListeners = 999
	if *g.Candidates[1].Popularity.UniqueListeners != 10 || *chosen.Candidates[0].Popularity.UniqueListeners != 10 {
		t.Fatal("correction mutated prior choice")
	}
	if _, ok := back.WithConfirmedIdentity(ReferenceArtist, "invented"); ok {
		t.Fatal("unoffered identity accepted")
	}
}
