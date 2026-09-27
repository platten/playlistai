package app

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type indexedSeedCatalog struct {
	ports.Catalog
	metas   map[string]core.TrackMeta
	tracks  []core.TrackRef
	audio   map[string]bool
	queried string
}

func (c *indexedSeedCatalog) ArtistRecordingsByMBID(ctx context.Context, id string, _ int) ([]core.TrackRef, error) {
	c.queried = id
	return c.tracks, ctx.Err()
}
func (c *indexedSeedCatalog) Meta(id string) (core.TrackMeta, bool) {
	v, ok := c.metas[id]
	return v, ok
}
func (c *indexedSeedCatalog) Vectors(id string) (ports.Vectors, bool) {
	if c.audio[id] {
		return ports.Vectors{Audio: []float32{1, 0}}, true
	}
	return ports.Vectors{}, false
}

func TestAutomaticSeedIndexIgnoresCompoundDisplayNameAndDiversifiesRelease(t *testing.T) {
	const artist = "11111111-1111-4111-8111-111111111111"
	cat := &indexedSeedCatalog{metas: map[string]core.TrackMeta{}, audio: map[string]bool{}}
	for _, id := range []string{"no-audio", "first", "same-album", "other-album", "duplicate"} {
		ref := core.TrackRef{ID: id, Artist: "Fela Kuti;Africa 70", Title: id, RecordingIdentity: id}
		if id == "duplicate" {
			ref.RecordingIdentity = "first"
		}
		album := "album-one"
		if id == "other-album" {
			album = "album-two"
		}
		cat.tracks = append(cat.tracks, ref)
		cat.metas[id] = core.TrackMeta{Ref: ref, Album: album, AlbumReliable: true}
		cat.audio[id] = id != "no-audio"
	}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Fela Kuti", Influence: core.InfluencePositive, Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: artist, Name: "Fela Kuti"}}}}}}
	got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, cat, automaticIdentityResolver{})
	if err != nil || cat.queried != artist || got.References[0].Resolution == nil {
		t.Fatalf("%+v %v", got, err)
	}
	reps := got.References[0].Resolution.Selected.Representatives
	if len(reps) != 4 || reps[0].TrackID != "first" || reps[1].TrackID != "other-album" {
		t.Fatalf("wrong identity/audio/release ordering: %+v", reps)
	}
}

type conflictingSeedCatalog struct {
	*indexedSeedCatalog
	ports.LibraryMetadataCatalog
	recording core.EnrichedTrack
}

func (c conflictingSeedCatalog) LibraryRecordingMetadata(ctx context.Context, _ string) (core.EnrichedTrack, bool, error) {
	return c.recording, true, ctx.Err()
}

func TestAutomaticIndexedSeedsRejectConflictingOrAmbiguousMetadata(t *testing.T) {
	const artist = "11111111-1111-4111-8111-111111111111"
	const other = "22222222-2222-4222-8222-222222222222"
	ref := core.TrackRef{ID: "seed", Artist: "Artist;Band", RecordingIdentity: "musicbrainz:" + other}
	base := &indexedSeedCatalog{metas: map[string]core.TrackMeta{"seed": {Ref: ref}}, tracks: []core.TrackRef{ref}}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Controls: core.IntentControls{RecommendationMode: core.Automatic}, References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist", Influence: core.InfluencePositive, Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: artist, Name: "Artist"}}}}}}
	good := core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{artist}, RecordingID: other}
	for _, test := range []struct {
		name   string
		change func(*core.EnrichedTrack)
	}{
		{"wrong row", func(r *core.EnrichedTrack) { r.Ref.ID = "different" }},
		{"ambiguous", func(r *core.EnrichedTrack) { r.IdentityStatus = core.ResolutionAmbiguous }},
		{"unmatched", func(r *core.EnrichedTrack) { r.Matched = false }},
		{"other artist", func(r *core.EnrichedTrack) { r.ArtistIDs = []string{other} }},
		{"other recording", func(r *core.EnrichedTrack) { r.RecordingID = artist }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := good
			test.change(&value)
			got, err := (&Container{}).prepareAutomaticIntent(context.Background(), intent, conflictingSeedCatalog{indexedSeedCatalog: base, recording: value}, automaticIdentityResolver{})
			if err != nil || got.References[0].TrackID != "" {
				t.Fatalf("conflicting seed admitted: %+v %v", got.References, err)
			}
		})
	}
}

type mertSeedCatalog struct {
	*indexedSeedCatalog
	ports.LibraryAudioCatalog
}

func (c mertSeedCatalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	if id == "mert" {
		return core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "mert-fixture"}, Values: []float32{1, 0}}, true, ctx.Err()
	}
	return core.LibraryVector{}, false, ctx.Err()
}
func TestAutomaticSeedPrefersCompatibleMERTAvailability(t *testing.T) {
	refs := []core.TrackRef{{ID: "missing"}, {ID: "mert"}}
	base := &indexedSeedCatalog{metas: map[string]core.TrackMeta{"missing": {Ref: refs[0]}, "mert": {Ref: refs[1]}}}
	got := selectAutomaticSeeds(context.Background(), mertSeedCatalog{indexedSeedCatalog: base}, refs, nil)
	if len(got) != 2 || got[0].ID != "mert" {
		t.Fatalf("audio seed preference lost: %+v", got)
	}
}
