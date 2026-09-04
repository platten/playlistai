package deejai

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// filter carries the dedup + constraint state for one walk. It mirrors upstream:
//
//   - exclude: everything a Search must not return (playlist ids + all seed ids)
//   - usedDisplay: "Artist - Title" strings already used (in similar mode the
//     picks are added too; in journey mode only the waypoints are, per upstream)
//   - excludeArtist / seedArtist: our own MusicIntent constraints
//   - noBackToBack: reject a candidate whose artist == the previous pick's
type filter struct {
	exclude       map[string]struct{}
	usedDisplay   map[string]struct{}
	excludeArtist map[string]struct{}
	seedArtist    map[string]struct{}
	noBackToBack  bool
}

func newFilter(intent core.MusicIntent, seeds []core.TrackRef) *filter {
	f := &filter{
		exclude:       make(map[string]struct{}, len(seeds)+intent.Count),
		usedDisplay:   make(map[string]struct{}, len(seeds)+intent.Count),
		excludeArtist: make(map[string]struct{}),
		seedArtist:    make(map[string]struct{}),
		noBackToBack:  intent.Constraints.NoRepeatArtistBackToBack,
	}
	for _, s := range seeds {
		f.exclude[s.ID] = struct{}{}
	}
	for _, a := range intent.Constraints.ArtistsExclude {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			f.excludeArtist[a] = struct{}{}
		}
	}
	if intent.Constraints.ExcludeSeedArtists {
		for _, s := range seeds {
			f.seedArtist[strings.ToLower(s.Artist)] = struct{}{}
		}
	}
	return f
}

// markUsed records a pick in similar mode: excluded by id, and its display
// string reserved.
func (f *filter) markUsed(ref core.TrackRef) {
	f.exclude[ref.ID] = struct{}{}
	f.usedDisplay[ref.Display()] = struct{}{}
}

// markUsedID records a pick by id only (journey mode; picks don't reserve their
// display string there, matching upstream).
func (f *filter) markUsedID(ref core.TrackRef) { f.exclude[ref.ID] = struct{}{} }

// reserveDisplay reserves a waypoint's display string (journey mode).
func (f *filter) reserveDisplay(ref core.TrackRef) { f.usedDisplay[ref.Display()] = struct{}{} }

func (f *filter) excludeIDs() map[string]struct{} { return f.exclude }

// pick returns the first match that survives every dedup rule, its rank in the
// match list, and ok=false if none do.
func (f *filter) pick(cat ports.Catalog, matches []ports.Match, prevArtist string) (core.TrackRef, int, bool) {
	for rank, m := range matches {
		meta, ok := cat.Meta(m.ID)
		if !ok {
			continue
		}
		ref := meta.Ref
		if _, dup := f.usedDisplay[ref.Display()]; dup {
			continue
		}
		lart := strings.ToLower(ref.Artist)
		if _, ex := f.excludeArtist[lart]; ex {
			continue
		}
		if _, sa := f.seedArtist[lart]; sa {
			continue
		}
		if f.noBackToBack && artistPrefix(ref.Display()) == prevArtist {
			continue
		}
		return ref, rank, true
	}
	return core.TrackRef{}, 0, false
}
