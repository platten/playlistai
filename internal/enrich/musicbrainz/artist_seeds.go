package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Provider coverage and the shared metadata deadline can stop these searches
// earlier. Neither limit is a claim of exhaustive artist-discography coverage.
const artistSeedTrackLimit = 100

type seedArtist struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Aliases []struct {
		Name string `json:"name"`
	} `json:"aliases"`
}

func (a seedArtist) names() []string {
	names := []string{a.Name}
	for _, alias := range a.Aliases {
		names = append(names, alias.Name)
	}
	return names
}

func seedNameKey(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFKD.String(value))
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}

func seedNameMatches(value string, names []string) bool {
	key := seedNameKey(value)
	if key == "" {
		return false
	}
	for _, name := range names {
		if seedNameKey(name) == key {
			return true
		}
	}
	return false
}

func (c *Client) resolveMissingArtists(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, p ports.Progress) core.MusicIntent {
	resolved := map[string]core.IntentReference{}
	resolve := func(ref core.IntentReference) core.IntentReference {
		if ref.Kind != core.ReferenceArtist || ref.Influence != core.InfluencePositive || ctx.Err() != nil {
			return ref
		}
		if ref.Resolution != nil && ref.Resolution.CatalogVersion == resolver.CatalogVersion() && ref.Resolution.Status == core.ResolutionResolved {
			return ref
		}
		local := resolver.ResolveReference(ref)
		if local.Status == core.ResolutionResolved {
			return ref
		}
		key := seedNameKey(ref.Query)
		if prior, ok := resolved[key]; ok {
			ref.TrackID, ref.Resolution = prior.TrackID, prior.Resolution
			return ref
		}
		if local.Status == core.ResolutionAmbiguous {
			ref = c.disambiguateArtist(ctx, ref, local, snapshot, p)
		} else {
			ref = c.findArtistSeed(ctx, ref, cat, resolver, snapshot, p)
		}
		resolved[key] = ref
		return ref
	}
	for _, group := range []*[]core.IntentReference{&intent.References, &intent.Journey.Waypoints} {
		refs := append([]core.IntentReference(nil), (*group)...)
		for i := range refs {
			refs[i] = resolve(refs[i])
		}
		*group = refs
	}
	if intent.Destination != nil {
		destination := resolve(*intent.Destination)
		intent.Destination = &destination
	}
	return intent
}

// A shortened name may match unrelated catalog artists. Resolve it only when
// a single MusicBrainz identity explicitly associates that name with a local
// candidate. Search rank, catalog size, and LLM guesses cannot establish this.
func (c *Client) disambiguateArtist(ctx context.Context, ref core.IntentReference, local core.ReferenceResolution, snapshot *core.KnowledgeSnapshot, p ports.Progress) core.IntentReference {
	p.Report("generation", 0, 0, fmt.Sprintf("Checking artist identity for %q", ref.Query))
	artist, ambiguous, err := c.findSeedArtist(ctx, ref.Query, snapshot, local.Alternatives...)
	if err != nil || ambiguous || artist.ID == "" {
		return ref
	}
	for _, candidate := range local.Alternatives {
		if !seedNameMatches(candidate.Artist, artist.names()) || len(candidate.Representatives) == 0 {
			continue
		}
		candidate.Evidence = append(candidate.Evidence, core.ResolutionEvidence{Match: "musicbrainz_alias", NormalizedQuery: seedNameKey(ref.Query), MatchedText: artist.ID + ": " + artist.Name})
		ref.TrackID = candidate.Representatives[0].TrackID
		ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: local.CatalogVersion, Selected: &candidate}
		snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Resolved %q to %q using MusicBrainz artist-name evidence.", ref.Query, candidate.Artist))
		return ref
	}
	return ref
}

func (c *Client) findArtistSeed(ctx context.Context, ref core.IntentReference, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, p ports.Progress) core.IntentReference {
	notice := func(detail string) {
		snapshot.Notices = append(snapshot.Notices, detail)
		p.Report("generation", 0, 0, detail)
	}
	notice(fmt.Sprintf("Artist %q was not found under that name in the local catalog. Looking up the artist and popular tracks online.", ref.Query))
	mbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	artist, ambiguous, err := c.findSeedArtist(mbCtx, ref.Query, snapshot)
	cancel()
	if ambiguous {
		notice(fmt.Sprintf("MusicBrainz has multiple artists matching %q. Add a specific track or album to identify the artist; no online seed was selected.", ref.Query))
		return ref
	}
	if err != nil || artist.ID == "" {
		if err != nil {
			if c.MetadataStatus().DiscogsConfigured {
				notice("MusicBrainz artist lookup was unavailable. Trying Discogs release tracklists.")
				if found, ok := c.discogsArtistSeed(ctx, ref, cat, resolver, snapshot); ok {
					return found
				}
			}
			notice(fmt.Sprintf("MusicBrainz artist lookup was unavailable: %v. Trying Deezer's artist search.", err))
		} else {
			notice(fmt.Sprintf("MusicBrainz did not identify %q. Trying Deezer's artist search.", ref.Query))
		}
		artist = seedArtist{Name: ref.Query}
	}
	checked := 0
	selectTrack := func(title string, names []string, source, order string) bool {
		checked++
		p.Report("generation", int64(checked), 0, fmt.Sprintf("Checking %s: %s — %s", order, artist.Name, title))
		track, ok := matchSeedRecording(ctx, cat, resolver, title, names)
		if !ok {
			return false
		}
		candidate := core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: artist.ID, Artist: artist.Name, Confidence: 1,
			Representatives: []core.WeightedTrack{{TrackID: track.ID, Weight: 1}},
			Evidence:        []core.ResolutionEvidence{{Match: "online_artist_recording", NormalizedQuery: seedNameKey(ref.Query), MatchedText: track.Display()}, {Match: "source", MatchedText: source}},
		}
		if candidate.EntityID == "" {
			candidate.EntityID = source
		}
		ref.TrackID = track.ID
		ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: resolver.CatalogVersion(), Selected: &candidate}
		notice(fmt.Sprintf("Found %q online as %q. Using %s as the catalog seed after checking %d recording(s), starting with Deezer's top tracks when available.", ref.Query, artist.Name, track.Display(), checked))
		return true
	}
	found, err := c.tryPopularArtistTracks(ctx, artist, snapshot, selectTrack)
	if found {
		return ref
	}
	if err != nil {
		notice(fmt.Sprintf("Deezer's popular-track lookup could not be completed: %v.", err))
	}
	if artist.ID != "" && ctx.Err() == nil {
		notice("No usable seed from the popular-track lookup. Checking additional MusicBrainz recordings; their order does not indicate popularity.")
		if c.tryOtherArtistTracks(ctx, artist, snapshot, selectTrack) {
			return ref
		}
	}
	if ctx.Err() != nil {
		notice(fmt.Sprintf("The online lookup for %q stopped at the metadata time limit or cancellation after checking %d recording(s). Retry or add a specific track reference.", ref.Query, checked))
	} else {
		notice(fmt.Sprintf("Could not find a verified catalog seed for %q after checking %d recording(s). The artist may be online while their recordings are absent from this catalog. Add a specific track or another artist reference.", ref.Query, checked))
	}
	return ref
}

func (c *Client) findSeedArtist(ctx context.Context, query string, snapshot *core.KnowledgeSnapshot, candidates ...core.ResolutionCandidate) (seedArtist, bool, error) {
	path := "/ws/2/artist?" + url.Values{"query": {`artist:"` + mbEscape(query) + `" OR alias:"` + mbEscape(query) + `"`}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return seedArtist{}, false, err
	}
	var page struct {
		Count   int          `json:"count"`
		Artists []seedArtist `json:"artists"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return seedArtist{}, false, err
	}
	snapshot.Sources = append(snapshot.Sources, c.base+path)
	var match seedArtist
	for _, artist := range page.Artists {
		if artist.ID == "" || !seedNameMatches(query, artist.names()) {
			continue
		}
		if len(candidates) > 0 {
			corroborated := false
			for _, candidate := range candidates {
				corroborated = corroborated || seedNameMatches(candidate.Artist, artist.names())
			}
			if !corroborated {
				continue
			}
		}
		if match.ID != "" && match.ID != artist.ID {
			return seedArtist{}, true, nil
		}
		match = artist
	}
	if page.Count > len(page.Artists) {
		return seedArtist{}, true, nil
	}
	return match, false, nil
}

type deezerSeedArtist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type deezerSeedTrack struct {
	ID           int64              `json:"id"`
	Title        string             `json:"title"`
	Artist       deezerSeedArtist   `json:"artist"`
	Contributors []deezerSeedArtist `json:"contributors"`
}

func (c *Client) deezerSeedGet(ctx context.Context, path string, target any, snapshot *core.KnowledgeSnapshot) error {
	raw, err := c.metadataGet(ctx, c.deezerBase, path, "deezer-seeds-v1:", c.deezerClient, false)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	snapshot.Sources = append(snapshot.Sources, c.deezerBase+path)
	return nil
}

func (c *Client) tryPopularArtistTracks(ctx context.Context, artist seedArtist, snapshot *core.KnowledgeSnapshot, selectTrack func(string, []string, string, string) bool) (bool, error) {
	path := "/search/artist?" + url.Values{"q": {artist.Name}, "limit": {"100"}}.Encode()
	var artists struct {
		Data  []deezerSeedArtist `json:"data"`
		Total int                `json:"total"`
	}
	if err := c.deezerSeedGet(ctx, path, &artists, snapshot); err != nil {
		return false, err
	}
	var selected deezerSeedArtist
	for _, candidate := range artists.Data {
		if candidate.ID <= 0 || !seedNameMatches(candidate.Name, artist.names()) {
			continue
		}
		if selected.ID != 0 && candidate.ID != selected.ID {
			return false, fmt.Errorf("more than one artist matches %q", artist.Name)
		}
		selected = candidate
	}
	if artists.Total > len(artists.Data) {
		return false, fmt.Errorf("artist search is too broad; specify a track or album")
	}
	if selected.ID == 0 {
		return false, fmt.Errorf("no matching artist found for %q", artist.Name)
	}
	seen := map[int64]bool{}
	for offset := 0; offset < artistSeedTrackLimit; {
		path = "/artist/" + strconv.FormatInt(selected.ID, 10) + "/top?" + url.Values{"limit": {strconv.Itoa(artistSeedTrackLimit - offset)}, "index": {strconv.Itoa(offset)}}.Encode()
		var page struct {
			Data []deezerSeedTrack `json:"data"`
			Next string            `json:"next"`
		}
		if err := c.deezerSeedGet(ctx, path, &page, snapshot); err != nil {
			return false, err
		}
		for _, track := range page.Data[:min(len(page.Data), artistSeedTrackLimit-offset)] {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if track.ID <= 0 || seen[track.ID] || strings.TrimSpace(track.Title) == "" {
				continue
			}
			seen[track.ID] = true
			credited := track.Artist.ID == selected.ID
			names := []string{track.Artist.Name}
			for _, credit := range track.Contributors {
				credited = credited || credit.ID == selected.ID
				names = append(names, credit.Name)
			}
			if !credited {
				continue
			}
			names = append(names, artist.names()...)
			if selectTrack(track.Title, names, c.deezerBase+path, "Deezer top track") {
				return true, nil
			}
		}
		offset += len(page.Data)
		if len(page.Data) == 0 || page.Next == "" {
			break
		}
	}
	return false, nil
}

func (c *Client) tryOtherArtistTracks(ctx context.Context, artist seedArtist, snapshot *core.KnowledgeSnapshot, selectTrack func(string, []string, string, string) bool) bool {
	for offset := 0; offset < artistSeedTrackLimit; {
		path := "/ws/2/recording?" + url.Values{"query": {"arid:" + artist.ID}, "fmt": {"json"}, "limit": {strconv.Itoa(artistSeedTrackLimit - offset)}, "offset": {strconv.Itoa(offset)}}.Encode()
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Additional recording lookup unavailable: %v.", err))
			return false
		}
		var page struct {
			Recordings []mbRecording `json:"recordings"`
			Count      int           `json:"count"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return false
		}
		snapshot.Sources = append(snapshot.Sources, c.base+path)
		for _, track := range page.Recordings[:min(len(page.Recordings), artistSeedTrackLimit-offset)] {
			if ctx.Err() != nil {
				return false
			}
			credited := false
			names := artist.names()
			for _, credit := range track.ArtistCredit {
				credited = credited || credit.Artist.ID == artist.ID
				names = append(names, credit.Name)
			}
			if credited && track.ID != "" && selectTrack(track.Title, names, c.base+path, "MusicBrainz recording") {
				return true
			}
		}
		offset += len(page.Recordings)
		if len(page.Recordings) == 0 || offset >= page.Count {
			break
		}
	}
	return false
}

// A title-only hit is insufficient: the catalog credit must agree with the
// provider credit or a MusicBrainz alias. Version words remain part of the title.
func matchSeedRecording(ctx context.Context, cat ports.Catalog, resolver ports.ReferenceResolver, title string, names []string) (core.TrackRef, bool) {
	if seedNameKey(title) == "" {
		return core.TrackRef{}, false
	}
	seen := map[string]bool{}
	queries := append(append([]string(nil), names...), "")
	for _, artist := range queries {
		if ctx.Err() != nil {
			return core.TrackRef{}, false
		}
		query := title
		if artist != "" {
			query = artist + " - " + title
		}
		if seen[query] {
			continue
		}
		seen[query] = true
		result := resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, Query: query})
		if result.Status != core.ResolutionResolved || result.Selected == nil {
			continue
		}
		for _, representative := range result.Selected.Representatives {
			meta, ok := cat.Meta(representative.TrackID)
			if !ok || seedNameKey(meta.Ref.Title) != seedNameKey(title) || !seedNameMatches(meta.Ref.Artist, names) {
				continue
			}
			if _, ok := cat.Vectors(meta.Ref.ID); ok {
				return meta.Ref, true
			}
		}
	}
	return core.TrackRef{}, false
}
