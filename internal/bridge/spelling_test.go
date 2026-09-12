package bridge

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/resolution"
)

type spellingResolver struct{}

func (spellingResolver) CatalogVersion() string { return "spelling-fixture" }
func (spellingResolver) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	r := core.ReferenceResolution{CatalogVersion: "spelling-fixture", Status: core.ResolutionUnresolved}
	if ref.SpellingDecision == "original" {
		return r
	}
	c := core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: "Christian Löffler", EntityID: "artist:christian loffler", Evidence: []core.ResolutionEvidence{{Match: "spelling"}}, Representatives: []core.WeightedTrack{{TrackID: "haul", Weight: 1}}}
	if ref.TrackID == "haul" && ref.SpellingDecision == "accepted" {
		r.Status, r.Selected = core.ResolutionResolved, &c
	} else {
		r.Status, r.Alternatives = core.ResolutionAmbiguous, []core.ResolutionCandidate{c}
	}
	return r
}

func TestArtistSpellingDecisionPreservesPromptAndExclusionEvidence(t *testing.T) {
	source := core.SourceEvidence{Text: "christrian loeffler", Start: 3, End: 21, Explicit: true}
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: source.Text, Influence: core.InfluenceNegative, Evidence: []core.SourceEvidence{source}}
	m := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "no christrian loeffler", References: []core.IntentReference{ref}, HardConstraints: []core.HardConstraint{{Kind: "exclude_artist", Value: ref.Query, Evidence: ref.Evidence}}}
	_, issues := resolution.Apply(spellingResolver{}, m)
	if len(issues) != 1 || issues[0].SpellingSuggestion == nil || !errors.Is(resolution.SpellingConfirmationError(issues), core.ErrAmbiguousReference) {
		t.Fatalf("missing confirmation: %+v", issues)
	}
	for _, reject := range []bool{false, true} {
		selection := ResolutionSelection{Kind: ref.Kind, Query: ref.Query, RejectSpelling: reject}
		if !reject {
			selection.TrackID = "haul"
		}
		choices, err := validateResolutionSelections(spellingResolver{}, m, []ResolutionSelection{selection})
		if err != nil {
			t.Fatal(err)
		}
		chosen := m
		chosen.References = applySelections(m.References, choices)
		got, issues := resolution.Apply(spellingResolver{}, chosen)
		if got.OriginalDescription != m.OriginalDescription || got.References[0].Query != ref.Query || !reflect.DeepEqual(got.References[0].Evidence, ref.Evidence) {
			t.Fatal("source wording was changed")
		}
		if reject {
			if got.References[0].SpellingDecision != "original" || got.References[0].TrackID != "" || len(got.Constraints.ArtistsExclude) != 1 || issues[0].SpellingSuggestion != nil {
				t.Fatalf("rejected name corrected: %+v", got)
			}
		} else if got.References[0].SpellingDecision != "accepted" || got.References[0].TrackID != "haul" || !reflect.DeepEqual(got.Constraints.ArtistsExclude, []string{ref.Query, "Christian Löffler"}) {
			t.Fatalf("accepted exclusion identity lost: %+v", got)
		}
		if _, again := resolution.Apply(spellingResolver{}, got.Normalized()); resolution.SpellingConfirmationError(again) != nil {
			t.Fatal("choice was lost on replay")
		}
	}
	if m.References[0].SpellingDecision != "" || m.References[0].TrackID != "" {
		t.Fatal("changed cached parse")
	}
}

func TestSpellingChoiceMustBeAnOfferedCatalogAlternative(t *testing.T) {
	m := core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "christrian loeffler"}}}
	for _, choice := range []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "christrian loeffler", TrackID: "invented"}, {Kind: core.ReferenceArtist, Query: "another name", TrackID: "haul"}, {Kind: core.ReferenceArtist, Query: "christrian loeffler", TrackID: "haul", RejectSpelling: true}} {
		if _, err := validateResolutionSelections(spellingResolver{}, m, []ResolutionSelection{choice}); err == nil {
			t.Fatal("invalid choice accepted", choice)
		}
	}
}

func TestGenerateRequiresArtistSpellingConfirmation(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	preview, err := a.ParseIntent(context.Background(), "like Daft Punq, 10 tracks")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.ResolutionIssues) != 1 || preview.ResolutionIssues[0].SpellingSuggestion == nil {
		t.Fatalf("missing spelling preview: %+v", preview.ResolutionIssues)
	}
	if _, err := a.GenerateFromPrompt(context.Background(), "like Daft Punq, 10 tracks"); !errors.Is(err, core.ErrAmbiguousReference) {
		t.Fatalf("unconfirmed artist generated: %v", err)
	}
	candidate := preview.ResolutionIssues[0].SpellingSuggestion
	generated, err := a.GenerateFromPromptResolved(context.Background(), "like Daft Punq, 10 tracks", []ResolutionSelection{{Kind: core.ReferenceArtist, Query: "Daft Punq", TrackID: candidate.Representatives[0].TrackID}})
	if err != nil {
		t.Fatal(err)
	}
	if generated.Request.Intent.References[0].SpellingDecision != "accepted" || generated.Request.Intent.References[0].Query != "Daft Punq" {
		t.Fatal("accepted decision lost")
	}
}
