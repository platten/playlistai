package lexicon_test

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

func TestReferenceStrengthSurvivesSourceReconciliationAndSavedIntent(t *testing.T) {
	for _, tc := range []struct{ prompt, strength string }{{"like Radiohead", "preferred"}, {"Radiohead only", "required"}} {
		intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, lexicon.Extract(tc.prompt)).Normalized()
		data, err := json.Marshal(intent)
		if err != nil {
			t.Fatal(err)
		}
		var saved core.MusicIntent
		if err = json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		saved = saved.Normalized()
		found := false
		for _, ref := range saved.References {
			if ref.Query == "Radiohead" {
				found = true
				if ref.Strength != tc.strength {
					t.Fatalf("%q strength=%q, wanted %q", tc.prompt, ref.Strength, tc.strength)
				}
			}
		}
		if !found {
			t.Fatalf("lost artist for %q: %+v", tc.prompt, saved.References)
		}
	}
	// Historical missing roles cannot acquire new discovery permission on load.
	old := core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Radiohead"}}}.Normalized()
	if old.References[0].Strength != "" {
		t.Fatal("reinterpreted legacy reference")
	}
}

func TestCompoundArtistSourceRoleSurvivesModelProtection(t *testing.T) {
	prompt := "like Simon and Garfunkel"
	intent := core.MusicIntent{OriginalDescription: prompt, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Simon and Garfunkel", Influence: core.InfluencePositive, Evidence: []core.SourceEvidence{{Text: "Simon and Garfunkel", Start: 5, End: len(prompt), Explicit: true}}}}}
	got := lexicon.Reconcile(intent, lexicon.Extract(prompt))
	if len(got.References) != 1 || got.References[0].Query != "Simon and Garfunkel" || got.References[0].Strength != "preferred" {
		t.Fatalf("compound discovery target lost role: %+v", got.References)
	}
}
