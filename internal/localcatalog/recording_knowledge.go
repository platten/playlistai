package localcatalog

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

// BindRecordingKnowledge attaches already resolved recording identity to this
// request-owned catalog. It does not mutate the bundled catalog or promote
// artist context into recording metadata. Ambiguous/conflicting matches abstain.
func (c *CompositeCatalog) BindRecordingKnowledge(tracks []core.EnrichedTrack) {
	if base, ok := c.base.(interface{ BindRecordingKnowledge([]core.EnrichedTrack) }); ok {
		base.BindRecordingKnowledge(tracks)
	}
	known := map[string]core.EnrichedTrack{}
	conflicts := map[string]bool{}
	for _, track := range tracks {
		id := track.Ref.ID
		if track.IdentityStatus != core.ResolutionResolved || librarypack.CanonicalMusicBrainzRecordingID(track.RecordingID) == "" || conflicts[id] {
			continue
		}
		meta, ok := c.base.Meta(id)
		if !ok || core.NormalizeIdentityPart(meta.Ref.Artist) != core.NormalizeIdentityPart(track.Ref.Artist) || core.NormalizeIdentityPart(meta.Ref.Title) != core.NormalizeIdentityPart(track.Ref.Title) {
			continue
		}
		identity := strings.ToLower(strings.TrimSpace(meta.Ref.RecordingIdentity))
		existingMBID := librarypack.CanonicalMBID(strings.TrimPrefix(identity, "musicbrainz:"))
		existingISRC := librarypack.CanonicalISRC(meta.ISRC)
		if value, found := strings.CutPrefix(identity, "isrc:"); found && existingISRC == "" {
			existingISRC = librarypack.CanonicalISRC(value)
		}
		incomingISRC := librarypack.CanonicalISRC(track.ISRC)
		if existingMBID != "" && !strings.EqualFold(existingMBID, track.RecordingID) || existingISRC != "" && incomingISRC != "" && existingISRC != incomingISRC {
			delete(known, id)
			conflicts[id] = true
			continue
		}
		if prior, exists := known[id]; exists && !strings.EqualFold(prior.RecordingID, track.RecordingID) || meta.MusicBrainzRecording != "" && !strings.EqualFold(meta.MusicBrainzRecording, track.RecordingID) {
			delete(known, id)
			conflicts[id] = true
			continue
		}
		known[id] = track
	}
	c.recordingKnowledge = known
}

func (c *CompositeCatalog) baseMetadata(id string) (core.TrackMeta, bool) {
	meta, ok := c.base.Meta(id)
	if !ok {
		return meta, false
	}
	if track, found := c.recordingKnowledge[id]; found {
		meta.MusicBrainzRecording = strings.ToLower(track.RecordingID)
		if meta.ISRC == "" {
			meta.ISRC = librarypack.CanonicalISRC(track.ISRC)
		}
	}
	return meta, true
}
