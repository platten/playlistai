package app

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

// previewRecordingReader supplies identity constraints, never musical proof.
// The request's pinned catalog includes discovery/local packs that are absent
// from the base runtime catalog. All reads are local and honor cancellation.
type previewRecordingReader struct {
	cached   ports.CachedRecordingReader
	fallback func() ports.Catalog
}

func (c *Container) previewRecordings() ports.CachedRecordingReader {
	reader, _ := c.Enrich.(ports.CachedRecordingReader)
	return &previewRecordingReader{cached: reader, fallback: func() ports.Catalog { return c.Runtime().Catalog }}
}

func (r *previewRecordingReader) CachedRecording(ref core.TrackRef) (core.EnrichedTrack, bool) {
	return r.CachedRecordingContext(context.Background(), ref)
}

func (r *previewRecordingReader) CachedRecordingContext(ctx context.Context, ref core.TrackRef) (core.EnrichedTrack, bool) {
	if ctx.Err() != nil {
		return core.EnrichedTrack{}, false
	}
	cat, pinned := ports.AudioMetadataCatalog(ctx)
	if !pinned && r.fallback != nil {
		cat = r.fallback()
	}
	var meta core.TrackMeta
	var found bool
	if cat != nil {
		meta, found = ports.CatalogMeta(ctx, cat, ref.ID)
	}
	if ctx.Err() != nil {
		return core.EnrichedTrack{}, false
	}
	conflict := func() (core.EnrichedTrack, bool) {
		return core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionAmbiguous}, true
	}
	if found && (meta.Ref.ID != ref.ID ||
		core.NormalizeIdentityPart(meta.Ref.Artist) != core.NormalizeIdentityPart(ref.Artist) ||
		core.NormalizeIdentityPart(meta.Ref.Title) != core.NormalizeIdentityPart(ref.Title) ||
		ref.RecordingIdentity != "" && meta.Ref.RecordingIdentity != "" && !strings.EqualFold(ref.RecordingIdentity, meta.Ref.RecordingIdentity)) {
		return conflict()
	}
	cached, hasCached := ports.CachedRecordingContext(ctx, r.cached, ref)
	if ctx.Err() != nil {
		return core.EnrichedTrack{}, false
	}
	if hasCached && core.ProvisionalRecordingKey(cached.Ref) != core.ProvisionalRecordingKey(ref) {
		return conflict()
	}
	if hasCached && cached.IdentityStatus == core.ResolutionAmbiguous {
		return conflict()
	}
	// Preserve only identity fields used by the preview resolver. Catalog facts
	// do not upgrade cached genre tags, acoustic predictions or source claims.
	out := core.EnrichedTrack{Ref: ref}
	if hasCached {
		out.Matched, out.IdentityStatus = cached.Matched, cached.IdentityStatus
		if cached.Matched && cached.IdentityStatus == core.ResolutionResolved {
			out.RecordingID = librarypack.CanonicalMusicBrainzRecordingID(cached.RecordingID)
			out.ISRC = librarypack.CanonicalISRC(cached.ISRC)
			out.Album = cached.Album
			if cached.FullRecordingDuration.Valid() {
				duration := *cached.FullRecordingDuration
				out.FullRecordingDuration = &duration
			}
		}
	}
	if !found {
		return out, hasCached
	}
	mbid := librarypack.CanonicalMusicBrainzRecordingID(meta.MusicBrainzRecording)
	isrc := librarypack.CanonicalISRC(meta.ISRC)
	if mbid != "" && out.RecordingID != "" && mbid != out.RecordingID ||
		isrc != "" && out.ISRC != "" && isrc != out.ISRC {
		return conflict()
	}
	if mbid != "" {
		out.RecordingID = mbid
	}
	if isrc != "" {
		out.ISRC = isrc
	}
	if meta.FullRecordingDuration.Valid() {
		duration := *meta.FullRecordingDuration
		if meta.SourceIdentity != "" && duration.RecordingID == meta.SourceIdentity {
			// The pinned row explicitly links this source to the requested
			// catalog recording. Carry that link through the narrower preview
			// DTO without exposing the source identity or changing catalog data.
			duration.RecordingID = ref.ID
		}
		out.FullRecordingDuration = &duration
	}
	if meta.AlbumReliable {
		out.Album = meta.Album
	}
	if mbid != "" || isrc != "" || meta.FullRecordingDuration.Valid() {
		out.Matched, out.IdentityStatus = true, core.ResolutionResolved
	}
	return out, true
}

var _ ports.ContextCachedRecordingReader = (*previewRecordingReader)(nil)
