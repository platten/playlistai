package core

import "testing"

func TestGenreGraphSeparatesAliasesParentsAndInfluences(t *testing.T) {
	graph := GenreGraph{Nodes: []GenreNode{{ID: "broad", Name: "Broad"}, {ID: "child", Name: "Child", Aliases: []string{"Alias"}}, {ID: "related", Name: "Related"}}, Relations: []GenreRelation{{From: "child", To: "broad", Kind: "subgenre"}, {From: "related", To: "child", Kind: "influence"}, {From: "broad", To: "child", Kind: "subgenre"}}}
	if !graph.Matches("Broad", "ALIAS") || graph.Matches("Child", "Related") || graph.Matches("missing", "Alias") {
		t.Fatal("genre graph lost aliases, relationship kinds or cycle safety")
	}
}
func TestIntentVerificationMigration(t *testing.T) {
	for _, tc := range []struct {
		version int
		want    VerificationPolicy
	}{{7, VerifiedOnly}, {8, BestAvailable}} {
		got := (MusicIntent{Version: tc.version}).Normalized()
		if got.VerificationPolicy != tc.want || got.Normalized().VerificationPolicy != tc.want {
			t.Fatalf("v%d policy = %s", tc.version, got.VerificationPolicy)
		}
	}
}
