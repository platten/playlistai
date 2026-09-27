package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

// Embedded interfaces fail immediately if preparation starts an unnecessary
// graph/catalog scan. Local artist identities are not MusicBrainz UUIDs.
type unusedAutomaticIdentityCatalog struct {
	ports.Catalog
	ports.LibraryMetadataCatalog
	ports.ArtistRecordingCatalog
	ports.RecordingIdentityCatalog
}

func TestAutomaticArtistPreparationPreservesLocalIdentity(t *testing.T) {
	for _, id := range []string{"paipack-artist:daft punk", "local-artist:daft punk", "not-a-mbid"} {
		t.Run(id, func(t *testing.T) {
			ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Daft Punk", TrackID: "catalog-seed", Influence: core.InfluencePositive,
				Grounding:  &core.IdentityGrounding{Provider: "MusicBrainz+paipack", Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: id, Name: "Daft Punk"}}},
				Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "fixture/v1", Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: "artist:daft punk", Artist: "Daft Punk", Representatives: []core.WeightedTrack{{TrackID: "catalog-seed", Weight: 1}}}},
			}
			intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{ref}}.Normalized()
			before, _ := json.Marshal(intent)
			got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, unusedAutomaticIdentityCatalog{}, automaticIdentityResolver{})
			if err != nil || !reflect.DeepEqual(got.References, intent.References) {
				t.Fatalf("local identity was replaced: %+v %v", got.References, err)
			}
			after, _ := json.Marshal(intent)
			if string(before) != string(after) {
				t.Fatal("preparation mutated the supplied local identity")
			}
		})
	}
}

func TestAutomaticArtistPreparationReusesCurrentExactIdentity(t *testing.T) {
	const selected = "11111111-1111-4111-8111-111111111111"
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Chosen Artist", Influence: core.InfluencePositive,
		Grounding:  &core.IdentityGrounding{Provider: "MusicBrainz", Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: selected, Name: "Chosen Artist"}}},
		Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "fixture/v1", Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: selected, Artist: "Chosen Artist", Representatives: []core.WeightedTrack{{TrackID: "authenticated-seed", Weight: 1}}}},
	}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{ref}}.Normalized()
	got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, unusedAutomaticIdentityCatalog{}, automaticIdentityResolver{})
	want := intent.Normalized()
	want.References[0].TrackID = "authenticated-seed"
	if err != nil || !reflect.DeepEqual(got.References, want.References) {
		t.Fatalf("current exact identity was needlessly replaced: %+v %v", got.References, err)
	}
	// Confirmed MusicBrainz identities require an authenticated explicit seed
	// through the real resolution step, including when saved TrackID was empty.
	confirmed := intent.Normalized()
	confirmed.References[0].Grounding.Confirmed = true
	prepared, err := (&Container{}).prepareAutomaticIntent(context.Background(), confirmed, unusedAutomaticIdentityCatalog{}, automaticIdentityResolver{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, issues := resolution.ApplyContext(context.Background(), automaticIdentityResolver{}, prepared)
	if len(issues) != 0 || resolved.References[0].TrackID != "authenticated-seed" || resolved.References[0].Resolution.Status != core.ResolutionResolved {
		t.Fatalf("authenticated confirmed representative lost in resolution: %+v %+v", resolved.References, issues)
	}
	for _, test := range []struct {
		name   string
		change func(*core.ReferenceResolution)
	}{
		{"stale catalog", func(r *core.ReferenceResolution) { r.CatalogVersion = "fixture/old" }},
		{"different artist", func(r *core.ReferenceResolution) { r.Selected.EntityID = "22222222-2222-4222-8222-222222222222" }},
		{"wrong kind", func(r *core.ReferenceResolution) { r.Selected.Kind = core.ReferenceTrack }},
		{"unresolved", func(r *core.ReferenceResolution) { r.Status = core.ResolutionUnresolved }},
		{"missing representatives", func(r *core.ReferenceResolution) { r.Selected.Representatives = nil }},
		{"empty representative", func(r *core.ReferenceResolution) { r.Selected.Representatives[0].TrackID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := intent.Normalized()
			test.change(changed.References[0].Resolution)
			catalog := automaticIdentityCatalog{tracks: []core.TrackRef{{ID: "current-authenticated-seed", Artist: "Chosen Artist"}}, ids: map[string][]string{"current-authenticated-seed": {selected}}}
			got, err := (&Container{}).prepareAutomaticIntent(context.Background(), changed, catalog, automaticIdentityResolver{})
			if err != nil || got.References[0].TrackID != "current-authenticated-seed" || got.References[0].Resolution.Selected.EntityID != selected {
				t.Fatalf("unusable identity was reused: %+v %v", got.References, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = (&Container{}).prepareAutomaticIntent(ctx, intent, unusedAutomaticIdentityCatalog{}, automaticIdentityResolver{})
	if err != context.Canceled || !reflect.DeepEqual(got.References, intent.References) {
		t.Fatalf("canceled preparation read or changed catalog identity: %+v %v", got.References, err)
	}
}

type automaticIdentityCatalog struct {
	ports.Catalog
	ports.LibraryMetadataCatalog
	tracks []core.TrackRef
	ids    map[string][]string
	reads  *[]string
}

func (c automaticIdentityCatalog) ArtistRecordings(ctx context.Context, _ string) ([]core.TrackRef, error) {
	return c.tracks, ctx.Err()
}
func (c automaticIdentityCatalog) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	if c.reads != nil {
		*c.reads = append(*c.reads, id)
	}
	ids, ok := c.ids[id]
	return core.EnrichedTrack{Ref: core.TrackRef{ID: id}, ArtistIDs: ids, Matched: true, IdentityStatus: core.ResolutionResolved}, ok, ctx.Err()
}

func TestAutomaticArtistPreparationChecksDirectCreditsBeforeBaseAliases(t *testing.T) {
	const selected = "11111111-1111-4111-8111-111111111111"
	const rival = "22222222-2222-4222-8222-222222222222"
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist", Influence: core.InfluencePositive,
		Grounding: &core.IdentityGrounding{Provider: "MusicBrainz", Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: selected, Name: "Artist"}}},
	}}}
	tracks := []core.TrackRef{{ID: "base-alias", Artist: "Artist"}, {ID: "pack:unknown", Artist: "Artist"}, {ID: "local:rival", Artist: "Artist"}, {ID: "pack:first", Artist: "Artist"}, {ID: "local:second", Artist: "Artist"}, {ID: "pack:third", Artist: "Artist"}, {ID: "pack:fourth", Artist: "Artist"}, {ID: "local:fifth", Artist: "Artist"}}
	ids := map[string][]string{"base-alias": {selected}, "local:rival": {rival}, "pack:first": {selected}, "local:second": {selected}, "pack:third": {selected}, "pack:fourth": {selected}, "local:fifth": {selected}}
	original := append([]core.TrackRef(nil), tracks...)
	for range 2 {
		var reads []string
		got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, automaticIdentityCatalog{tracks: tracks, ids: ids, reads: &reads}, automaticIdentityResolver{})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"pack:first", "local:second", "pack:third", "pack:fourth", "local:fifth"}
		var chosen []string
		for _, rep := range got.References[0].Resolution.Selected.Representatives {
			chosen = append(chosen, rep.TrackID)
		}
		if !reflect.DeepEqual(chosen, want) || !reflect.DeepEqual(reads, append([]string{"pack:unknown", "local:rival"}, want...)) {
			t.Fatalf("wrong identities or redundant base metadata: chosen=%v reads=%v", chosen, reads)
		}
		if !reflect.DeepEqual(tracks, original) {
			t.Fatal("preparation changed the catalog's shared reference order")
		}
	}
	var reads []string
	var bounded []core.TrackRef
	for i := range 513 {
		bounded = append(bounded, core.TrackRef{ID: "pack:" + strconv.Itoa(i), Artist: "Artist"})
	}
	got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, automaticIdentityCatalog{tracks: bounded, ids: map[string][]string{"pack:512": {selected}}, reads: &reads}, automaticIdentityResolver{})
	if err != nil || len(reads) != 512 || got.References[0].Resolution != nil {
		t.Fatalf("fallback exceeded its existing total budget: reads=%d references=%+v err=%v", len(reads), got.References, err)
	}
}

type automaticIdentityResolver struct{ ports.ReferenceResolver }

func (automaticIdentityResolver) CatalogVersion() string { return "fixture/v1" }

func TestAutomaticArtistPreparationRequiresIdentityCredits(t *testing.T) {
	const selected = "11111111-1111-4111-8111-111111111111"
	const namesake = "22222222-2222-4222-8222-222222222222"
	grounding := &core.IdentityGrounding{Provider: "MusicBrainz", SnapshotVersion: "fixture", MatchedSpelling: "Shared Name", MatchType: "canonical", Candidates: []core.IdentityCandidate{{ID: selected, Name: "Shared Name", Kind: core.ReferenceArtist, MatchType: "canonical"}}}
	grounding.Decision = core.DecideArtist(grounding)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Shared Name", Influence: core.InfluencePositive, Grounding: grounding}}}
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "namesake only", true: "identified recording"}[available], func(t *testing.T) {
			catalog := automaticIdentityCatalog{tracks: []core.TrackRef{{ID: "rival", Artist: "Shared Name"}, {ID: "unknown", Artist: "Shared Name"}}, ids: map[string][]string{"rival": {namesake}}}
			if available {
				catalog.tracks = append(catalog.tracks, core.TrackRef{ID: "right", Artist: "Shared Name"})
				catalog.ids["right"] = []string{selected}
			}
			got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, catalog, automaticIdentityResolver{})
			if err != nil {
				t.Fatal(err)
			}
			if !available && got.References[0].Resolution != nil {
				t.Fatal("name match resolved the wrong or unknown identity")
			}
			if available && (got.References[0].TrackID != "right" || got.References[0].Resolution.Selected.EntityID != selected) {
				t.Fatal("prepared artist did not retain exact credits")
			}
			if intent.References[0].Resolution != nil || intent.References[0].TrackID != "" {
				t.Fatal("preparation mutated the supplied intent")
			}
		})
	}
}
