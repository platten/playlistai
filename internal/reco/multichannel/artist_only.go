package multichannel

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// A strict artist-only request is grounded in the resolved artist identity,
// never an artist substring in a track title or an inferred anchor.
func requiredArtist(intent core.MusicIntent, value string) (string, bool) {
	for _, ref := range intent.References {
		if ref.Kind != core.ReferenceArtist || ref.Influence == core.InfluenceNegative || !strings.EqualFold(ref.Query, value) {
			continue
		}
		if ref.Resolution != nil && ref.Resolution.Status == core.ResolutionResolved && ref.Resolution.Selected != nil {
			return ref.Resolution.Selected.Artist, ref.Resolution.Selected.Artist != ""
		}
	}
	return "", false
}

func artistOnlyEligible(intent core.MusicIntent, track core.TrackRef) bool {
	for _, c := range intent.HardConstraints {
		if c.Kind == "require_artist" {
			artist, ok := requiredArtist(intent, c.Value)
			if !ok || core.NormalizeIdentityPart(track.Artist) != core.NormalizeIdentityPart(artist) {
				return false
			}
		}
	}
	return true
}

func artistRestrictionConflict(intent core.MusicIntent) []core.OutcomeReason {
	only := ""
	for _, c := range intent.HardConstraints {
		if c.Kind != "require_artist" {
			continue
		}
		artist, ok := requiredArtist(intent, c.Value)
		if !ok {
			continue
		}
		key := core.NormalizeIdentityPart(artist)
		conflict := only != "" && only != key || intent.Constraints.ExcludeSeedArtists || intent.Count > 1 && intent.Constraints.NoRepeatArtistBackToBack
		for _, excluded := range intent.Constraints.ArtistsExclude {
			conflict = conflict || key == core.NormalizeIdentityPart(excluded)
		}
		if conflict {
			return []core.OutcomeReason{{Code: "artist_only_conflict", Detail: "The artist-only restriction conflicts with another artist restriction or exclusion.", Criterion: c.Value, Action: "remove the conflicting artist restriction or exclusion"}}
		}
		only = key
	}
	return nil
}

func artistOnlyCandidates(ctx context.Context, cat ports.Catalog, intent core.MusicIntent) ([]core.TrackRef, error) {
	for _, c := range intent.HardConstraints {
		if c.Kind != "require_artist" {
			continue
		}
		artist, ok := requiredArtist(intent, c.Value)
		if !ok {
			return nil, nil
		}
		if indexed, ok := cat.(ports.ArtistRecordingCatalog); ok {
			return indexed.ArtistRecordings(ctx, artist)
		}
		var tracks []core.TrackRef
		for row := 0; row < cat.Len(); row++ {
			if row%256 == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if meta, ok := cat.Meta(cat.ID(row)); ok && core.NormalizeIdentityPart(meta.Ref.Artist) == core.NormalizeIdentityPart(artist) {
				tracks = append(tracks, meta.Ref)
			}
		}
		return tracks, nil
	}
	return nil, nil
}
