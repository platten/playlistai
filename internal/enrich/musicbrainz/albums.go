package musicbrainz

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Deezer is a documented metadata fallback when MusicBrainz cannot resolve an
// album. Only exact artist/title identities and corroborated recordings qualify.
func (c *Client) resolveDeezerAlbum(ctx context.Context, ref core.IntentReference, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) core.IntentReference {
	title, artist := ref.Query, ""
	if a, t, ok := core.QualifiedReferenceParts(ref.Query); ok {
		artist, title = a, t
	}
	path := "/search/album?" + url.Values{"q": {strings.TrimSpace(title + " " + artist)}, "limit": {"25"}}.Encode()
	var page struct {
		Total int `json:"total"`
		Data  []struct {
			ID     int64            `json:"id"`
			Title  string           `json:"title"`
			Artist deezerSeedArtist `json:"artist"`
		} `json:"data"`
	}
	if err := c.deezerSeedGet(ctx, path, &page, snapshot); err != nil {
		snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Deezer album lookup for %q was unavailable: %v.", ref.Query, err))
		return ref
	}
	result := core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: resolver.CatalogVersion()}
	seen := map[int64]bool{}
	for _, album := range page.Data[:min(len(page.Data), 25)] {
		if album.ID <= 0 || seen[album.ID] || seedNameKey(album.Title) != seedNameKey(title) || album.Artist.Name == "" || artist != "" && seedNameKey(album.Artist.Name) != seedNameKey(artist) {
			continue
		}
		seen[album.ID] = true
		result.Alternatives = append(result.Alternatives, core.ResolutionCandidate{Kind: core.ReferenceAlbum, EntityID: "deezer:album:" + strconv.FormatInt(album.ID, 10), Artist: album.Artist.Name, Title: album.Title, Confidence: 1,
			Evidence: []core.ResolutionEvidence{{Match: "exact", NormalizedQuery: ref.Query, MatchedText: album.Artist.Name + " - " + album.Title}, {Match: "source", MatchedText: c.deezerBase + path}}})
	}
	ref.Resolution = &result
	if len(result.Alternatives) > 1 || page.Total > len(page.Data) {
		result.Status = core.ResolutionAmbiguous
		snapshot.Notices = append(snapshot.Notices, "The album search is ambiguous or incomplete. Add the artist and exact album title to narrow the lookup.")
		return ref
	}
	if len(result.Alternatives) == 0 {
		return ref
	}
	candidate := result.Alternatives[0]
	id := strings.TrimPrefix(candidate.EntityID, "deezer:album:")
	var tracks struct {
		Data []deezerSeedTrack `json:"data"`
	}
	trackPath := "/album/" + id + "/tracks?limit=100"
	if err := c.deezerSeedGet(ctx, trackPath, &tracks, snapshot); err != nil {
		snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("The album was found on Deezer, but its recordings could not be read: %v.", err))
		return ref
	}
	used := map[string]bool{}
	for _, recording := range tracks.Data[:min(len(tracks.Data), 100)] {
		if ctx.Err() != nil {
			break
		}
		track, ok := matchSeedRecording(ctx, cat, resolver, recording.Title, []string{recording.Artist.Name})
		if !ok || used[track.ID] {
			continue
		}
		used[track.ID] = true
		candidate.Representatives = append(candidate.Representatives, core.WeightedTrack{TrackID: track.ID})
		snapshot.Candidates = append(snapshot.Candidates, track)
	}
	if len(candidate.Representatives) == 0 {
		return ref
	}
	for i := range candidate.Representatives {
		candidate.Representatives[i].Weight = 1 / float64(len(candidate.Representatives))
	}
	candidate.Evidence = append(candidate.Evidence, core.ResolutionEvidence{Match: "source", MatchedText: c.deezerBase + trackPath})
	result.Status, result.Selected, result.Alternatives = core.ResolutionResolved, &candidate, nil
	ref.TrackID = candidate.Representatives[0].TrackID
	snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Resolved %q through Deezer's album metadata and %d corroborated catalog recording(s). Previews still require musical assessment.", ref.Query, len(candidate.Representatives)))
	return ref
}
