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
	"github.com/platten/playlistai/internal/librarypack"
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
		if ref.Kind != core.ReferenceArtist || ref.Influence != core.InfluencePositive || ref.SpellingDecision == "original" || ctx.Err() != nil {
			return ref
		}
		if ref.Resolution != nil && ref.Resolution.CatalogVersion == resolver.CatalogVersion() && ref.Resolution.Status == core.ResolutionResolved {
			return ref
		}
		local := ports.ResolveReferenceContext(ctx, resolver, ref)
		if ctx.Err() != nil {
			return ref
		}
		groundedArtist, grounded := groundedSeedArtist(ref)
		if local.Status == core.ResolutionResolved && !grounded {
			return ref
		}
		key := seedNameKey(ref.Query)
		if prior, ok := resolved[key]; ok {
			ref.TrackID, ref.Resolution = prior.TrackID, prior.Resolution
			return ref
		}
		if grounded {
			ref = c.findArtistSeedForIdentity(ctx, ref, groundedArtist, cat, resolver, snapshot, p)
		} else if local.Status == core.ResolutionAmbiguous {
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
	for _, endpoint := range []**core.IntentReference{&intent.Start, &intent.Destination} {
		if *endpoint != nil {
			ref := resolve(**endpoint)
			*endpoint = &ref
		}
	}
	return intent
}

func groundedSeedArtist(ref core.IntentReference) (seedArtist, bool) {
	if candidate, ok := ref.Grounding.CorroboratedArtist(); ok {
		return seedArtist{ID: candidate.ID, Name: candidate.Name}, true
	}
	if ref.Grounding == nil || ref.Grounding.Truncated || len(ref.Grounding.Candidates) != 1 {
		return seedArtist{}, false
	}
	candidate := ref.Grounding.Candidates[0]
	if candidate.Kind != core.ReferenceArtist || strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Name) == "" {
		return seedArtist{}, false
	}
	return seedArtist{ID: candidate.ID, Name: candidate.Name}, true
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
			notice(fmt.Sprintf("MusicBrainz artist lookup was unavailable: %v. Trying Deezer's artist search.", err))
		} else {
			notice(fmt.Sprintf("MusicBrainz did not identify %q. Trying Deezer's artist search.", ref.Query))
		}
		artist = seedArtist{Name: ref.Query}
	}
	return c.findArtistSeedForIdentity(ctx, ref, artist, cat, resolver, snapshot, p)
}

func (c *Client) findArtistSeedForIdentity(ctx context.Context, ref core.IntentReference, artist seedArtist, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, p ports.Progress) core.IntentReference {
	_, corroborated := ref.Grounding.CorroboratedArtist()
	pinnedIdentity := ref.Grounding != nil && ref.Grounding.Confirmed || corroborated
	notice := func(detail string) {
		snapshot.Notices = append(snapshot.Notices, detail)
		p.Report("generation", 0, 0, detail)
	}
	if indexed, ok := cat.(ports.ArtistIdentityCatalog); ok && artist.ID != "" {
		tracks, err := indexed.ArtistRecordingsByMBID(ctx, artist.ID, 512)
		if err == nil {
			candidate := core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: artist.ID, Artist: artist.Name, Confidence: 1,
				Evidence: []core.ResolutionEvidence{{Match: "recording_artist_identity", MatchedText: artist.ID}}}
			seen := map[string]bool{}
			for _, track := range tracks {
				if ctx.Err() != nil {
					return ref
				}
				if _, available := cat.Vectors(track.ID); !available {
					continue
				}
				key := track.RecordingIdentity
				if key == "" {
					key = track.ID
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				candidate.Representatives = append(candidate.Representatives, core.WeightedTrack{TrackID: track.ID})
				if len(candidate.Representatives) == 5 {
					break
				}
			}
			if len(candidate.Representatives) > 0 {
				for i := range candidate.Representatives {
					candidate.Representatives[i].Weight = 1 / float64(len(candidate.Representatives))
				}
				ref.TrackID = candidate.Representatives[0].TrackID
				ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: resolver.CatalogVersion(), Selected: &candidate}
				return ref
			}
		}
	}
	// A provider alias can bridge scripts even when the user's spelling is not
	// present in the recommendation catalog (for example a native-script name
	// whose catalog credit is romanized). Require an exact, uniquely resolved
	// local artist and provider-backed alias; fuzzy catalog search alone is not
	// identity evidence.
	for _, alias := range artist.Aliases {
		if pinnedIdentity {
			break // A namesake alias cannot authenticate a chosen artist's seed.
		}
		if seedNameMatches(alias.Name, []string{ref.Query}) {
			continue
		}
		local := ports.ResolveReferenceContext(ctx, resolver, core.IntentReference{Kind: core.ReferenceArtist, Query: alias.Name, Influence: ref.Influence})
		if ctx.Err() != nil {
			return ref
		}
		if local.Status != core.ResolutionResolved || local.Selected == nil || len(local.Selected.Representatives) == 0 || !seedNameMatches(local.Selected.Artist, []string{alias.Name}) {
			continue
		}
		candidate := *local.Selected
		candidate.EntityID = artist.ID
		candidate.Confidence = 1
		candidate.Evidence = append(candidate.Evidence, core.ResolutionEvidence{Match: "musicbrainz_alias", NormalizedQuery: seedNameKey(ref.Query), MatchedText: artist.ID + ": " + alias.Name})
		ref.TrackID = candidate.Representatives[0].TrackID
		ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: resolver.CatalogVersion(), Selected: &candidate}
		notice(fmt.Sprintf("Resolved %q to catalog artist %q using a MusicBrainz alias.", ref.Query, candidate.Artist))
		return ref
	}
	checked := 0
	selectTrack := func(recording mbRecording, names []string, source, order string) bool {
		checked++
		p.Report("generation", int64(checked), 0, fmt.Sprintf("Checking %s: %s — %s", order, artist.Name, recording.Title))
		var identity []seedRecordingIdentity
		if pinnedIdentity {
			identity = []seedRecordingIdentity{{artistID: artist.ID, recording: recording, requirePositive: corroborated}}
		}
		track, ok := matchSeedRecording(ctx, cat, resolver, recording.Title, names, identity...)
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
		notice(fmt.Sprintf("Found %q as %q. Using %s as the catalog seed after checking %d recording(s) from %s.", ref.Query, artist.Name, track.Display(), checked, source))
		return true
	}
	if artist.ID != "" {
		if offline := c.localMusicBrainz(); offline != nil {
			if rows, err := offline.ArtistRecordings(ctx, artist.ID, artistSeedTrackLimit, 0); err == nil {
				for _, track := range rows {
					names := append([]string{track.ArtistCredit}, artist.names()...)
					if selectTrack(offlineRecording(track), names, "offline MusicBrainz snapshot", "MusicBrainz recording") {
						return ref
					}
				}
			}
		}
	}
	// A same-name Deezer result does not authenticate a MusicBrainz identity
	// explicitly chosen among homonyms. Keep that choice pinned to MBID-backed
	// recordings even when the local snapshot contains no usable catalog seed.
	if !pinnedIdentity {
		found, err := c.tryPopularArtistTracks(ctx, artist, snapshot, func(title string, names []string, source, order string) bool {
			return selectTrack(mbRecording{Title: title}, names, source, order)
		})
		if found {
			return ref
		}
		if err != nil {
			notice(fmt.Sprintf("Deezer's popular-track lookup could not be completed: %v.", err))
		}
	}
	if artist.ID != "" && ctx.Err() == nil {
		notice("Checking additional MusicBrainz recordings for the artist identity; their order does not indicate popularity.")
		if c.tryOtherArtistTracks(ctx, artist, snapshot, selectTrack) {
			return ref
		}
	}
	if ctx.Err() != nil {
		notice(fmt.Sprintf("The online lookup for %q stopped at the metadata time limit or cancellation after checking %d recording(s). Retry or add a specific track reference.", ref.Query, checked))
	} else if checked == 0 {
		notice(fmt.Sprintf("Could not inspect any recordings for %q. The artist lookup returned no credited recordings or was unavailable. Retry or add a specific track reference.", ref.Query))
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

func (c *Client) tryOtherArtistTracks(ctx context.Context, artist seedArtist, snapshot *core.KnowledgeSnapshot, selectTrack func(mbRecording, []string, string, string) bool) bool {
	for offset := 0; offset < artistSeedTrackLimit; {
		// Browse is scoped directly to the confirmed artist MBID. Recording
		// search depends on the separate search index and can return no hits
		// even when the artist has linked recordings.
		path := artistRecordingPagePath(artist.ID, offset, artistSeedTrackLimit-offset)
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Additional recording lookup unavailable: %v.", err))
			return false
		}
		var page struct {
			Recordings []mbRecording `json:"recordings"`
			Count      int           `json:"recording-count"`
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
			if credited && track.ID != "" && selectTrack(track, names, c.base+path, "MusicBrainz recording") {
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
func matchSeedRecording(ctx context.Context, cat ports.Catalog, resolver ports.ReferenceResolver, title string, names []string, identities ...seedRecordingIdentity) (core.TrackRef, bool) {
	if seedNameKey(title) == "" {
		return core.TrackRef{}, false
	}
	seen := map[string]bool{}
	queries := append(append([]string(nil), names...), "")
	var provisional core.TrackRef
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
		result := ports.ResolveReferenceContext(ctx, resolver, core.IntentReference{Kind: core.ReferenceTrack, Query: query})
		if ctx.Err() != nil {
			return core.TrackRef{}, false
		}
		if len(identities) == 0 && (result.Status != core.ResolutionResolved || result.Selected == nil) {
			continue
		}
		var representatives []core.WeightedTrack
		if result.Selected != nil {
			representatives = append(representatives, result.Selected.Representatives...)
		}
		selectedCount := len(representatives)
		if len(identities) > 0 {
			for _, alternative := range result.Alternatives {
				representatives = append(representatives, alternative.Representatives...)
			}
		}
		for i, representative := range representatives {
			meta, ok := ports.CatalogMeta(ctx, cat, representative.TrackID)
			if !ok || seedNameKey(meta.Ref.Title) != seedNameKey(title) || !seedNameMatches(meta.Ref.Artist, names) {
				continue
			}
			if _, ok := cat.Vectors(meta.Ref.ID); ok {
				if len(identities) == 0 {
					return meta.Ref, true
				}
				exact, conflict := identities[0].matches(meta)
				if conflict {
					continue
				}
				if exact {
					return meta.Ref, true
				}
				// Legacy user-confirmed references retain a no-ID name/title
				// fallback. Automatic corroboration requires positive IDs above.
				// Neither fallback may break an ambiguous recording result.
				if result.Status == core.ResolutionResolved && i < selectedCount && provisional.ID == "" {
					provisional = meta.Ref
				}
			}
		}
	}
	return provisional, provisional.ID != "" && ctx.Err() == nil
}

type seedRecordingIdentity struct {
	artistID        string
	recording       mbRecording
	requirePositive bool
}

func (s seedRecordingIdentity) matches(meta core.TrackMeta) (exact, conflict bool) {
	wantRecording := librarypack.CanonicalMusicBrainzRecordingID(s.recording.ID)
	recordings := []string{meta.MusicBrainzRecording, strings.TrimPrefix(meta.Ref.RecordingIdentity, "musicbrainz:")}
	var artistIDs []string
	for _, annotation := range meta.Annotations {
		if annotation.Kind != "artist_mbid" && annotation.Kind != "recording_mbid" {
			continue
		}
		values := strings.FieldsFunc(annotation.Value, func(r rune) bool { return r == ';' || r == ',' || unicode.IsSpace(r) })
		if annotation.Kind == "artist_mbid" {
			artistIDs = append(artistIDs, values...)
		} else {
			recordings = append(recordings, values...)
		}
	}
	for _, value := range recordings {
		if id := librarypack.CanonicalMusicBrainzRecordingID(value); id != "" {
			if id != wantRecording {
				return false, true
			}
			exact = true
		}
	}
	knownArtist, matchingArtist := false, false
	for _, value := range artistIDs {
		if id := librarypack.CanonicalMusicBrainzRecordingID(value); id != "" {
			knownArtist = true
			matchingArtist = matchingArtist || strings.EqualFold(id, s.artistID)
		}
	}
	return exact, knownArtist && !matchingArtist || s.requirePositive && !exact && !matchingArtist || conflictingRecordingISRCs(core.EnrichedTrack{ISRC: meta.ISRC}, s.recording.ISRCs)
}
