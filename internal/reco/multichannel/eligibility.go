package multichannel

import (
	"context"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

type eligibility struct {
	excludedIDs        map[string]struct{}
	excludedRecordings map[string]struct{}
	excludedArtists    map[string]struct{}
	referenceArtists   map[string]struct{}
}

func newEligibility(intent core.MusicIntent, references, required []core.TrackRef) *eligibility {
	e := &eligibility{
		excludedIDs: make(map[string]struct{}), excludedRecordings: make(map[string]struct{}),
		excludedArtists: make(map[string]struct{}), referenceArtists: make(map[string]struct{}),
	}
	for _, reference := range references {
		e.excludedIDs[reference.ID] = struct{}{}
		e.excludedRecordings[core.ProvisionalRecordingKey(reference)] = struct{}{}
		e.referenceArtists[core.NormalizeIdentityPart(reference.Artist)] = struct{}{}
	}
	for _, track := range required {
		e.excludedIDs[track.ID] = struct{}{}
		e.excludedRecordings[core.ProvisionalRecordingKey(track)] = struct{}{}
	}
	for _, artist := range intent.Constraints.ArtistsExclude {
		if key := core.NormalizeIdentityPart(artist); key != "" {
			e.excludedArtists[key] = struct{}{}
		}
	}
	return e
}

func (e *eligibility) excludeRecent(tracks []core.TrackRef) {
	for _, track := range tracks {
		e.excludedIDs[track.ID] = struct{}{}
		e.excludedRecordings[core.ProvisionalRecordingKey(track)] = struct{}{}
	}
}

func (e *eligibility) validateRequired(required []core.TrackRef, excludeReferenceArtists bool) error {
	for _, track := range required {
		if e.hardExcluded(track, excludeReferenceArtists) {
			return fmt.Errorf("%w: %q is required and excluded", core.ErrRequiredTrackConflict, track.Display())
		}
	}
	return nil
}

func (e *eligibility) filter(ctx context.Context, candidates []core.Candidate, excludeReferenceArtists bool) ([]core.Candidate, error) {
	result := make([]core.Candidate, 0, len(candidates))
	seenRecordings := make(map[string]struct{}, len(candidates))
	for index, candidate := range candidates {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if _, excluded := e.excludedIDs[candidate.Track.ID]; excluded {
			continue
		}
		recording := core.ProvisionalRecordingKey(candidate.Track)
		if _, excluded := e.excludedRecordings[recording]; excluded {
			continue
		}
		if _, duplicate := seenRecordings[recording]; duplicate {
			continue
		}
		if e.hardExcluded(candidate.Track, excludeReferenceArtists) {
			continue
		}
		seenRecordings[recording] = struct{}{}
		result = append(result, candidate)
	}
	return result, nil
}

func (e *eligibility) hardExcluded(track core.TrackRef, excludeReferenceArtists bool) bool {
	artist := core.NormalizeIdentityPart(track.Artist)
	if _, excluded := e.excludedArtists[artist]; excluded {
		return true
	}
	if excludeReferenceArtists {
		_, excluded := e.referenceArtists[artist]
		return excluded
	}
	return false
}
