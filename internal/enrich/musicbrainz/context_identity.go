package musicbrainz

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// A shared artist name is insufficient identity evidence. Resolve ambiguity
// only when exactly one bounded candidate matches at least two distinct actual
// catalog recording titles, with its MBID present in their artist credits.
// Search score, biography genres and popularity never break identity ties.
func (c *Client) contextArtistIdentity(ctx context.Context, selected core.ResolutionCandidate, cat ports.Catalog) (seedArtist, []core.ContextSource, bool) {
	artist, ambiguous, err := c.findSeedArtist(ctx, selected.Artist, &core.KnowledgeSnapshot{})
	if err != nil {
		return seedArtist{}, nil, false
	}
	if !ambiguous {
		return artist, nil, artist.ID != ""
	}
	path := "/ws/2/artist?" + url.Values{"query": {`artist:"` + mbEscape(selected.Artist) + `" OR alias:"` + mbEscape(selected.Artist) + `"`}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return seedArtist{}, nil, false
	}
	var page struct {
		Count   int          `json:"count"`
		Artists []seedArtist `json:"artists"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Count > len(page.Artists) {
		return seedArtist{}, nil, false
	}
	var candidates []seedArtist
	seen := map[string]bool{}
	for _, candidate := range page.Artists {
		if contextMBID.MatchString(candidate.ID) && seedNameMatches(selected.Artist, candidate.names()) && !seen[candidate.ID] {
			seen[candidate.ID] = true
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) < 2 || len(candidates) > 3 {
		return seedArtist{}, nil, false
	}
	var titles []string
	keys := map[string]bool{}
	for _, representative := range selected.Representatives {
		meta, ok := cat.Meta(representative.TrackID)
		if !ok || !seedNameMatches(selected.Artist, []string{meta.Ref.Artist}) {
			continue
		}
		key := core.NormalizeIdentityPart(meta.Ref.Title)
		if key == "" || keys[key] {
			continue
		}
		keys[key] = true
		titles = append(titles, `recording:"`+mbEscape(meta.Ref.Title)+`"`)
		if len(titles) == 4 {
			break
		}
	}
	if len(titles) < 2 {
		return seedArtist{}, nil, false
	}
	sort.Strings(titles)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	sources := []core.ContextSource{contextMBSource(c.base+path, raw)}
	var match seedArtist
	for _, candidate := range candidates {
		query := "arid:" + candidate.ID + " AND (" + strings.Join(titles, " OR ") + ")"
		path = "/ws/2/recording?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}.Encode()
		raw, err = c.knowledgeGet(ctx, path, false)
		if err != nil {
			return seedArtist{}, nil, false
		} // a missing rival response is not negative evidence
		var recordings struct {
			Count      int           `json:"count"`
			Recordings []mbRecording `json:"recordings"`
		}
		if json.Unmarshal(raw, &recordings) != nil {
			return seedArtist{}, nil, false
		}
		sources = append(sources, contextMBSource(c.base+path, raw))
		matched := map[string]bool{}
		for _, recording := range recordings.Recordings[:min(len(recordings.Recordings), 100)] {
			key := core.NormalizeIdentityPart(recording.Title)
			if keys[key] && recording.ID != "" && contextArtistCredit(recording.ArtistCredit, candidate.ID, selected.Artist) {
				matched[key] = true
			}
		}
		if len(matched) >= 2 {
			if match.ID != "" {
				return seedArtist{}, nil, false
			}
			match = candidate
		} else if recordings.Count > len(recordings.Recordings) {
			// Positive corroboration survives truncation. A below-threshold
			// rival needs a complete response before absence can rule it out.
			return seedArtist{}, nil, false
		}
	}
	return match, sources, match.ID != ""
}
