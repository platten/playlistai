package bridge

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/resolution"
)

func TestDescriptionChoicePreservesMusicAndRemovesArtistOnReplay(t *testing.T) {
	for _, influence := range []core.Influence{core.InfluencePositive, core.InfluenceNegative} {
		t.Run(string(influence), func(t *testing.T) {
			constraintKind := "require_artist"
			if influence == core.InfluenceNegative {
				constraintKind = "exclude_artist"
			}
			evidence := []core.SourceEvidence{{Text: "dreamy", Start: 0, End: 6, Explicit: true}}
			ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "dreamy", Influence: influence, Evidence: evidence}
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "dreamy electronic like Justice, 7 tracks",
				References:      []core.IntentReference{ref, {Kind: core.ReferenceArtist, Query: "Justice", Influence: core.InfluencePositive}},
				Controls:        core.IntentControls{TotalTrackCount: 7},
				Preferences:     core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}}},
				HardConstraints: []core.HardConstraint{{Kind: constraintKind, Value: "dreamy"}, {Kind: "exclude_artist", Value: "Other artist"}},
				Translation:     &core.IntentTranslation{Version: "test", Atoms: []core.IntentAtom{{Kind: "artist", Value: "dreamy", Scope: "playlist", Polarity: string(influence), Evidence: evidence}}},
			}
			before, _ := json.Marshal(intent)
			choices, err := validateResolutionSelections(spellingResolver{}, intent, []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "dreamy", KeepAsDescription: true}})
			if err != nil {
				t.Fatal(err)
			}
			got := applyDescriptionSelections(intent, choices)
			if len(got.References) != 1 || got.References[0].Query != "Justice" || len(got.Preferences.Moods) != 1 || got.Preferences.Moods[0].Value != "dreamy" || got.Preferences.Moods[0].Influence != influence {
				t.Fatalf("artist was not converted to mood: %+v", got)
			}
			if got.Controls.TotalTrackCount != 7 || got.OriginalDescription != intent.OriginalDescription || len(got.Preferences.Genres) != 1 || len(got.HardConstraints) == 0 || got.HardConstraints[len(got.HardConstraints)-1].Value != "Other artist" {
				t.Fatalf("unrelated intent changed: %+v", got)
			}
			if got.Preferences.Moods[0].Strength != "required" || influence == core.InfluenceNegative && got.HardConstraints[0].Kind != "exclude_mood" {
				t.Fatal("requirement was weakened into a soft preference")
			}
			if influence == core.InfluencePositive && (len(got.EssentialCriteria) != 2 || got.EssentialCriteria[1].Kind != "mood") {
				t.Fatal("artist-only policy did not become a required musical criterion")
			}
			if got.Translation.Atoms[0].Kind != "mood" || !reflect.DeepEqual(got.Preferences.Moods[0].Evidence, evidence) {
				t.Fatal("source interpretation lost")
			}
			after, _ := json.Marshal(intent)
			if string(before) != string(after) {
				t.Fatal("modified cached parse")
			}
			stored, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var restored core.MusicIntent
			if err := json.Unmarshal(stored, &restored); err != nil {
				t.Fatal(err)
			}
			replayed, issues := resolution.Apply(spellingResolver{}, restored.Normalized())
			for _, issue := range issues {
				if issue.Query == "dreamy" {
					t.Fatal("description prompted as artist on replay")
				}
			}
			if len(replayed.Preferences.Moods) != 1 {
				t.Fatal("replay lost description")
			}
		})
	}
}

func TestDescriptionChoiceMustReferToOfferedExplicitArtist(t *testing.T) {
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "velvety", Influence: core.InfluencePositive}
	intent := core.MusicIntent{References: []core.IntentReference{ref}}
	for _, invalid := range []ResolutionSelection{
		{Kind: core.ReferenceArtist, Query: "absent", KeepAsDescription: true},
		{Kind: core.ReferenceTrack, Query: "velvety", KeepAsDescription: true},
		{Kind: core.ReferenceArtist, Query: "velvety", TrackID: "haul", KeepAsDescription: true},
		{Kind: core.ReferenceArtist, Query: "velvety", IdentityID: "id", KeepAsDescription: true},
		{Kind: core.ReferenceArtist, Query: "velvety", RejectSpelling: true, KeepAsDescription: true},
	} {
		if _, err := validateResolutionSelections(spellingResolver{}, intent, []ResolutionSelection{invalid}); err == nil {
			t.Fatalf("accepted invalid choice: %+v", invalid)
		}
	}
	choice := ResolutionSelection{Kind: core.ReferenceArtist, Query: "velvety", KeepAsDescription: true}
	if _, err := validateResolutionSelections(spellingResolver{}, core.MusicIntent{InferredAnchors: []core.InferredAnchor{{Reference: ref}}}, []ResolutionSelection{choice}); err == nil {
		t.Fatal("accepted inferred-only choice")
	}
	intent.References[0].Grounding = &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{ID: "a"}, {ID: "b"}}}
	choices, err := validateResolutionSelections(spellingResolver{}, intent, []ResolutionSelection{choice})
	if err != nil {
		t.Fatal(err)
	}
	got := applyDescriptionSelections(intent, choices)
	if len(got.References) != 0 || len(got.Preferences.Styles) != 1 || got.Preferences.Styles[0].Value != "velvety" || len(got.Seeds.Queries) != 0 {
		t.Fatalf("unknown description became a seed: %+v", got)
	}
}

func TestGenerateWithDescriptionChoiceDoesNotReinterpretCachedArtist(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	prompt := "like Justice and Daft Punq, 10 tracks"
	preview, err := a.ParseIntent(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := a.GenerateFromPromptResolved(context.Background(), prompt, []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "Daft Punq", KeepAsDescription: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range generated.Request.Intent.References {
		if ref.Query == "Daft Punq" {
			t.Fatal("generation retained mistaken artist")
		}
	}
	if len(generated.Request.Intent.Preferences.Styles) != 1 || generated.Request.Intent.Preferences.Styles[0].Value != "Daft Punq" {
		t.Fatalf("generation lost description: %+v", generated.Request.Intent)
	}
	again, err := a.ParseIntent(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Intent, again.Intent) {
		t.Fatal("description choice poisoned cached parse")
	}
}

func TestDescriptionChoiceKeepsJourneyScope(t *testing.T) {
	start := core.IntentReference{Kind: core.ReferenceArtist, Query: "dreamy", Influence: core.InfluencePositive}
	end := core.IntentReference{Kind: core.ReferenceArtist, Query: "Justice", Influence: core.InfluencePositive}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeJourney,
		Start: &start, Destination: &end, References: []core.IntentReference{start, end},
		Journey:     core.JourneyPlan{Waypoints: []core.IntentReference{start, end}},
		Translation: &core.IntentTranslation{Atoms: []core.IntentAtom{{Kind: "start", Value: "dreamy", Scope: "journey_start", Strength: "required", Polarity: "positive"}}},
	}
	got := applyDescriptionSelections(intent, []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "dreamy", KeepAsDescription: true}})
	if got.Start != nil || got.Destination == nil || got.Destination.Query != "Justice" || len(got.References) != 1 || len(got.Journey.Waypoints) != 1 {
		t.Fatalf("mistaken endpoint survived or real endpoint lost: %+v", got)
	}
	if len(got.Preferences.Moods) != 1 || got.Preferences.Moods[0].Scope != "journey_start" || len(got.EssentialCriteria) != 1 || got.EssentialCriteria[0].Scope != "journey_start" {
		t.Fatalf("opening description broadened to the whole playlist: %+v", got)
	}
}
