package app

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type previewMetadataFixture struct {
	*fakes.Catalog
	meta   core.TrackMeta
	cancel context.CancelFunc
}

func (c *previewMetadataFixture) MetaContext(ctx context.Context, _ string) (core.TrackMeta, bool) {
	if c.cancel != nil {
		c.cancel()
	}
	return c.meta, ctx.Err() == nil
}

type previewCachedFixture struct {
	track core.EnrichedTrack
	reads int
}

func (r *previewCachedFixture) CachedRecording(core.TrackRef) (core.EnrichedTrack, bool) {
	r.reads++
	return r.track, r.track.IdentityStatus != ""
}

func TestPreviewRecordingConstraintsUsePinnedCatalog(t *testing.T) {
	const mbid = "d4606a1b-8c34-4477-8887-bf0ea8c8ea41"
	ref := core.TrackRef{ID: "pack:fixture:local:recording", Artist: "Anthrax", Title: "Got the Time", RecordingIdentity: "mbid:" + mbid}
	meta := core.TrackMeta{Ref: ref, MusicBrainzRecording: mbid, ISRC: "US-IR2-90-00064", Album: "Live release", AlbumReliable: true,
		FullRecordingDuration: &core.RecordingDuration{Milliseconds: 222533, Source: "pack-stream", RecordingID: mbid}}
	for _, test := range []struct {
		name     string
		cached   core.EnrichedTrack
		conflict bool
	}{
		{name: "catalog-only identity"},
		{name: "compatible cache", cached: core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: mbid, ISRC: "USIR29000064", GenreTags: []core.AttributedGenreTag{{Name: "metal"}}}},
		{name: "different recording", conflict: true, cached: core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}},
		{name: "different ISRC", conflict: true, cached: core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved, ISRC: "GBAAA9800322"}},
		{name: "different cached ref", conflict: true, cached: core.EnrichedTrack{Ref: core.TrackRef{ID: "other", Artist: ref.Artist, Title: ref.Title}, Matched: true, IdentityStatus: core.ResolutionResolved}},
		{name: "cached ambiguity", conflict: true, cached: core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionAmbiguous}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cached := &previewCachedFixture{track: test.cached}
			reader := &previewRecordingReader{cached: cached, fallback: func() ports.Catalog { t.Fatal("pinned catalog fell back to base runtime"); return nil }}
			cat := &previewMetadataFixture{Catalog: fakes.NewCatalog(1), meta: meta}
			got, ok := reader.CachedRecordingContext(ports.WithAudioMetadataCatalog(context.Background(), cat), ref)
			if !ok || got.Ref != ref || cached.reads != 1 {
				t.Fatalf("identity context lost: %+v ok=%t reads=%d", got, ok, cached.reads)
			}
			if test.conflict {
				if got.Matched || got.IdentityStatus != core.ResolutionAmbiguous {
					t.Fatalf("conflicting identity fell back to name lookup: %+v", got)
				}
				return
			}
			if !got.Matched || got.IdentityStatus != core.ResolutionResolved || got.RecordingID != mbid || got.ISRC != "USIR29000064" || !got.FullRecordingDuration.Valid() || got.FullRecordingDuration.Milliseconds != 222533 || got.Album != meta.Album {
				t.Fatalf("catalog constraints lost: %+v", got)
			}
			if len(got.GenreTags) != 0 || len(got.Claims) != 0 || got.Acoustic != nil {
				t.Fatal("preview identity added musical proof")
			}
			got.FullRecordingDuration.Milliseconds = 1
			if meta.FullRecordingDuration.Milliseconds != 222533 {
				t.Fatal("preview reader mutated pinned catalog")
			}
		})
	}
}

func TestPreviewRecordingMetadataCancellationSkipsCachedFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ref := core.TrackRef{ID: "track", Artist: "Artist", Title: "Song"}
	cat := &previewMetadataFixture{Catalog: fakes.NewCatalog(1), meta: core.TrackMeta{Ref: ref}, cancel: cancel}
	cached := &previewCachedFixture{track: core.EnrichedTrack{Ref: ref, Matched: true, IdentityStatus: core.ResolutionResolved}}
	reader := &previewRecordingReader{cached: cached, fallback: func() ports.Catalog { return cat }}
	got, ok := reader.CachedRecordingContext(ctx, ref)
	if ok || got.Ref.ID != "" || cached.reads != 0 || ctx.Err() != context.Canceled {
		t.Fatalf("canceled metadata fell back to cache: %+v %t reads=%d", got, ok, cached.reads)
	}
}

func TestPreviewRecordingSourceDurationRemainsBoundToPinnedRow(t *testing.T) {
	ref := core.TrackRef{ID: "pack:fixture:local:recording", Artist: "Artist", Title: "Song"}
	for _, source := range []string{"source-key", "unrelated-key"} {
		meta := core.TrackMeta{Ref: ref, SourceIdentity: "source-key", FullRecordingDuration: &core.RecordingDuration{Milliseconds: 222533, Source: "local:stream", RecordingID: source}}
		cat := &previewMetadataFixture{Catalog: fakes.NewCatalog(1), meta: meta}
		reader := &previewRecordingReader{}
		got, ok := reader.CachedRecordingContext(ports.WithAudioMetadataCatalog(context.Background(), cat), ref)
		want := source
		if source == meta.SourceIdentity {
			want = ref.ID
		}
		if !ok || !got.FullRecordingDuration.Valid() || got.FullRecordingDuration.RecordingID != want {
			t.Fatalf("source duration binding lost or invented: %+v", got)
		}
		if meta.FullRecordingDuration.RecordingID != source {
			t.Fatal("preview transport changed source catalog provenance")
		}
	}
}
