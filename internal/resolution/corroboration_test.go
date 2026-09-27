package resolution

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func corroboratedReferenceFixture() core.IntentReference {
	const selected = "aaaaaaaa-1111-4111-8111-111111111111"
	const anchor = "cccccccc-3333-4333-8333-333333333333"
	return core.IntentReference{Kind: core.ReferenceArtist, Query: "Shared Artist", Influence: core.InfluencePositive, Grounding: &core.IdentityGrounding{
		Provider: "MusicBrainz", MatchedSpelling: "Shared Artist", MatchType: "canonical", SnapshotVersion: "fixture-v1",
		Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: selected, Name: "Shared Artist"}, {Kind: core.ReferenceArtist, ID: "bbbbbbbb-2222-4222-8222-222222222222", Name: "Shared Artist"}},
		Corroboration: &core.IdentityCorroboration{SelectedID: selected, Method: core.ArtistCoperformanceMethod, Supports: []core.IdentityCorroborationSupport{{
			AnchorID: anchor, RecordingIDs: []string{"eeeeeeee-5555-4555-8555-555555555555", "ffffffff-6666-4666-8666-666666666666"},
			Source: core.ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org/ws/2/artist/" + anchor + "?inc=recording-rels&fmt=json", Revision: strings.Repeat("ab", 32), License: "CC0-1.0"},
		}}},
	}}
}

func TestCorroboratedIdentityStillRequiresAnAuthenticatedCatalogSeed(t *testing.T) {
	for _, state := range []string{"name-only match", "combined provider", "catalog unavailable", "malformed proof", "no proof"} {
		t.Run(state, func(t *testing.T) {
			ref := corroboratedReferenceFixture()
			r := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "catalog-v2", Selected: &core.ResolutionCandidate{
				Kind: core.ReferenceArtist, EntityID: "catalog-homonym", Artist: "Shared Artist", Representatives: []core.WeightedTrack{{TrackID: "same-name-track", Weight: 1}},
			}}}
			want := core.ResolutionUnresolved
			switch state {
			case "combined provider":
				ref.Grounding.Provider = "MusicBrainz+paipack"
			case "catalog unavailable":
				r.result = core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: "catalog-v2"}
			case "malformed proof":
				ref.Grounding.Corroboration.Supports[0].Source.Revision = "mutable"
				want = core.ResolutionAmbiguous
			case "no proof":
				ref.Grounding.Corroboration = nil
				want = core.ResolutionAmbiguous
			}
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, References: []core.IntentReference{ref}}
			got, issues := ApplyContext(context.Background(), r, intent)
			resolved := got.References[0]
			if resolved.TrackID != "" || resolved.Resolution == nil || resolved.Resolution.Selected != nil || resolved.Resolution.Status != want || len(issues) != 1 || len(issues[0].GroundingCandidates) != 2 {
				t.Fatalf("name-only catalog result bypassed provider recovery: ref=%+v issues=%+v", resolved, issues)
			}
			if ref.Resolution != nil || !reflect.DeepEqual(ref.Grounding, resolved.Grounding) || resolved.Grounding.Confirmed {
				t.Fatal("resolution rewrote proof, candidate universe, or user choice")
			}
		})
	}
}

func TestCorroborationPreservesRecoveredSeedAndExplicitUserChoice(t *testing.T) {
	for _, choice := range []string{"recovered recording", "explicit track", "confirmed rival identity"} {
		t.Run(choice, func(t *testing.T) {
			ref := corroboratedReferenceFixture()
			selected := ref.Grounding.Corroboration.SelectedID
			if choice == "confirmed rival identity" {
				ref.Grounding.Confirmed = true
				ref.Grounding.Candidates = ref.Grounding.Candidates[1:]
				selected = ref.Grounding.Candidates[0].ID
			}
			r := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "catalog-v2", Selected: &core.ResolutionCandidate{
				Kind: core.ReferenceArtist, EntityID: selected, Artist: "Shared Artist", Representatives: []core.WeightedTrack{{TrackID: "verified-or-selected-recording", Weight: 1}},
			}}}
			ref.TrackID = "verified-or-selected-recording"
			if choice != "explicit track" {
				ref.Resolution = &r.result
			}
			resolved, issues := applyList(context.Background(), r, []core.IntentReference{ref}, false, nil)
			if len(issues) != 0 || resolved[0].TrackID != ref.TrackID || resolved[0].Resolution.Selected.EntityID != selected {
				t.Fatalf("corroboration displaced explicit or recovered choice: %+v %+v", resolved, issues)
			}
			if !reflect.DeepEqual(resolved[0].Grounding, ref.Grounding) {
				t.Fatal("resolution changed original grounding")
			}
		})
	}
}

func TestCanceledResolutionPreservesCorroborationWithoutMissingReferenceIssue(t *testing.T) {
	ref := corroboratedReferenceFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &testResolver{}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, References: []core.IntentReference{ref}}.Normalized()
	got, issues := ApplyContext(ctx, r, intent)
	if r.calls != 0 || len(issues) != 0 || !reflect.DeepEqual(intent, got) {
		t.Fatalf("cancellation changed corroborated intent: calls=%d issues=%+v", r.calls, issues)
	}
}
