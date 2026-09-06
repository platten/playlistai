package deejai

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// recordingKeyFunc is deliberately replaceable: artist/title is the best key
// in today's catalog, while a future catalog can supply canonical recording IDs.
type recordingKeyFunc func(core.TrackRef) string

type filter struct {
	exclude       map[string]struct{}
	usedRecording map[string]struct{}
	excludeArtist map[string]struct{}
	seedArtist    map[string]struct{}
	noBackToBack  bool
	recordingKey  recordingKeyFunc
}

func newFilter(intent core.MusicIntent, references, required []core.TrackRef) *filter {
	f := &filter{
		exclude:       make(map[string]struct{}, len(references)+len(required)+intent.Count),
		usedRecording: make(map[string]struct{}, len(references)+len(required)+intent.Count),
		excludeArtist: make(map[string]struct{}),
		seedArtist:    make(map[string]struct{}),
		noBackToBack:  intent.Constraints.NoRepeatArtistBackToBack,
		recordingKey:  provisionalRecordingKey,
	}
	for _, ref := range append(append([]core.TrackRef(nil), references...), required...) {
		f.exclude[ref.ID] = struct{}{}
		f.usedRecording[f.recordingKey(ref)] = struct{}{}
	}
	for _, artist := range intent.Constraints.ArtistsExclude {
		if key := normalizeIdentityPart(artist); key != "" {
			f.excludeArtist[key] = struct{}{}
		}
	}
	if intent.Constraints.ExcludeSeedArtists {
		for _, ref := range references {
			f.seedArtist[normalizeIdentityPart(ref.Artist)] = struct{}{}
		}
	}
	return f
}

func provisionalRecordingKey(ref core.TrackRef) string {
	return normalizeIdentityPart(ref.Artist) + "\x00" + normalizeIdentityPart(ref.Title)
}

func normalizeIdentityPart(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func (f *filter) hardExcluded(ref core.TrackRef) bool {
	artist := normalizeIdentityPart(ref.Artist)
	_, artistExcluded := f.excludeArtist[artist]
	_, seedArtistExcluded := f.seedArtist[artist]
	return artistExcluded || seedArtistExcluded
}

func (f *filter) markUsed(ref core.TrackRef) {
	f.exclude[ref.ID] = struct{}{}
	f.usedRecording[f.recordingKey(ref)] = struct{}{}
}

func (f *filter) excludeIDs() map[string]struct{} { return f.exclude }

// pick returns the first match that survives every hard exclusion and dedup rule.
func (f *filter) pick(cat ports.Catalog, matches []ports.Match, prevArtist string) (core.TrackRef, int, bool) {
	for rank, match := range matches {
		meta, ok := cat.Meta(match.ID)
		if !ok {
			continue
		}
		ref := meta.Ref
		if _, duplicate := f.usedRecording[f.recordingKey(ref)]; duplicate {
			continue
		}
		if f.hardExcluded(ref) {
			continue
		}
		if f.noBackToBack && normalizeIdentityPart(ref.Artist) == prevArtist {
			continue
		}
		return ref, rank, true
	}
	return core.TrackRef{}, 0, false
}
