package musicbrainz

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type discogsCandidates struct {
	initialized bool
	releases    []int64
	lastError   error
	queries     []discogsPageCursor
	used        map[int64]bool
	read        int
}

type discogsPageCursor struct {
	query url.Values
	page  int
	done  bool
}

func (f *discogsCandidates) hasMorePages() bool {
	if f.read >= discogsDiscoveryReleases {
		return false
	}
	for _, q := range f.queries {
		if !q.done {
			return true
		}
	}
	return false
}

func (s *candidateStream) loadDiscogsPages(ctx context.Context) {
	f := s.fallback
	var pools [][]int64
	for i := range f.queries {
		q := &f.queries[i]
		if q.done || ctx.Err() != nil {
			continue
		}
		q.page++
		page, err := s.client.discogsSearchPage(ctx, q.query, q.page)
		if err == nil && q.page == 1 && len(page.Results) == 0 && q.query.Has("genre") {
			q.query = url.Values{"style": {q.query.Get("genre")}}
			page, err = s.client.discogsSearchPage(ctx, q.query, q.page)
		}
		if err != nil {
			f.lastError = err
			q.done = true
			continue
		}
		q.done = q.page >= discogsSearchPages || page.FetchedResults == 0 || (page.Pagination.Pages <= q.page && page.Pagination.Items <= q.page*discogsPageSize)
		if q.page >= discogsSearchPages && (page.Pagination.Pages > q.page || page.Pagination.Items > q.page*discogsPageSize) {
			s.snapshot.Notices = append(s.snapshot.Notices, "Discogs search reached its three-page limit; additional search results were not fetched.")
		}
		var pool []int64
		for _, i := range s.rng.Perm(len(page.Results)) {
			r := page.Results[i]
			if r.ID > 0 && r.Type == "release" && !f.used[r.ID] {
				pool = append(pool, r.ID)
				f.used[r.ID] = true
			}
		}
		pools = append(pools, pool)
	}
	for round := 0; round < discogsPageSize; round++ {
		for _, pool := range pools {
			if round < len(pool) {
				f.releases = append(f.releases, pool[round])
			}
		}
	}
}

// Discogs provides a bounded discovery fallback, not a genre-verification
// shortcut. Release genres are intentionally not promoted to recording tags.
func (s *candidateStream) nextDiscogs(ctx context.Context) (core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return core.TrackRef{}, err
	}
	f := s.fallback
	if !f.initialized {
		f.initialized = true
		f.used = make(map[int64]bool)
		if len(s.genres) > discogsReleaseLimit {
			s.snapshot.Notices = append(s.snapshot.Notices, "Discogs fallback can search at most eight genre phrases per request; narrow the description if coverage is insufficient.")
		}
		for _, genre := range s.genres[:min(len(s.genres), discogsReleaseLimit)] {
			f.queries = append(f.queries, discogsPageCursor{query: url.Values{"genre": {genre}}})
		}
		s.loadDiscogsPages(ctx)
	}
	for len(f.releases) > 0 || len(s.pending) > 0 || f.hasMorePages() {
		if err := ctx.Err(); err != nil {
			return core.TrackRef{}, err
		}
		var tracks []core.TrackRef
		if f.read >= discogsDiscoveryReleases && len(f.releases) > 0 {
			f.releases = nil
			s.snapshot.Notices = append(s.snapshot.Notices, "Discogs discovery reached its 60-release limit; remaining provider results were not fetched.")
		}
		if len(f.releases) == 0 && len(s.pending) == 0 {
			if !f.hasMorePages() {
				break
			}
			s.loadDiscogsPages(ctx)
			continue
		}
		if len(f.releases) > 0 {
			id := f.releases[0]
			f.releases = f.releases[1:]
			f.read++
			release, err := s.client.discogsRelease(ctx, id)
			if err != nil {
				f.lastError = err
				continue
			}
			s.snapshot.Sources = append(s.snapshot.Sources, discogsSource(id))
			matches := discogsCatalogTracks(ctx, release, s.cat, s.resolver, s.intent.Constraints.ArtistsExclude)
			for _, i := range s.rng.Perm(len(matches)) {
				track := matches[i]
				key := core.ProvisionalRecordingKey(track)
				if !s.seen[key] {
					tracks = append(tracks, track)
					s.seen[key] = true
				}
			}
		} else {
			tracks, s.pending = s.pending[0], s.pending[1:]
		}
		if len(tracks) == 0 {
			continue
		}
		if len(tracks) > 1 {
			s.pending = append(s.pending, tracks[1:])
		}
		s.snapshot.Discovery = append(s.snapshot.Discovery, tracks[0])
		return tracks[0], nil
	}
	if err := ctx.Err(); err != nil {
		return core.TrackRef{}, err
	}
	if f.lastError != nil {
		return core.TrackRef{}, fmt.Errorf("discogs fallback incomplete: %w", f.lastError)
	}
	return core.TrackRef{}, io.EOF
}

func discogsCatalogTracks(ctx context.Context, release discogsRelease, cat ports.Catalog, resolver ports.ReferenceResolver, excluded []string) []core.TrackRef {
	var out []core.TrackRef
	for _, recording := range release.Tracklist {
		if ctx.Err() != nil {
			break
		}
		if recording.Type != "track" {
			continue
		} // no headings, medleys or index titles
		artists := recording.Artists
		if len(artists) == 0 {
			artists = release.Artists
		}
		names := discogsNames(artists)
		blocked := false
		for _, name := range names {
			blocked = blocked || excludedArtist(name, excluded)
		}
		if blocked {
			continue
		}
		if track, ok := matchSeedRecording(ctx, cat, resolver, recording.Title, names); ok {
			out = append(out, track)
		}
	}
	return out
}

// Used only on provider failure, never to override an explicit ambiguous or
// negative MusicBrainz result. These are catalog identities, not MBIDs/ISRCs.
func (c *Client) discogsArtistSeed(ctx context.Context, ref core.IntentReference, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) (core.IntentReference, bool) {
	page, err := c.discogsSearch(ctx, url.Values{"artist": {ref.Query}})
	if err != nil {
		return ref, false
	}
	for _, result := range page.Results[:min(len(page.Results), discogsReleaseLimit)] {
		if result.Type != "release" {
			continue
		}
		release, err := c.discogsRelease(ctx, result.ID)
		if err != nil {
			continue
		}
		if !seedNameMatches(ref.Query, discogsNames(release.Artists)) {
			continue
		}
		for _, track := range discogsCatalogTracks(ctx, release, cat, resolver, nil) {
			// Compilations/guest tracks must not substitute another performer.
			if !seedNameMatches(track.Artist, []string{ref.Query}) {
				continue
			}
			source := discogsSource(release.ID)
			candidate := core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: source, Artist: track.Artist,
				Representatives: []core.WeightedTrack{{TrackID: track.ID, Weight: 1}},
				Evidence:        []core.ResolutionEvidence{{Match: "discogs_catalog_recording", MatchedText: track.Display()}, {Match: "source", MatchedText: source}},
			}
			ref.TrackID = track.ID
			ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: resolver.CatalogVersion(), Selected: &candidate}
			snapshot.Sources = append(snapshot.Sources, source)
			snapshot.Candidates = append(snapshot.Candidates, track)
			snapshot.Notices = append(snapshot.Notices, "MusicBrainz was unavailable; Discogs supplied a catalog-matched artist recording. Musical fit is checked separately.")
			return ref, true
		}
	}
	return ref, false
}

func (c *Client) resolveDiscogsAlbum(ctx context.Context, ref core.IntentReference, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) (core.IntentReference, bool) {
	title, artist := ref.Query, ""
	if a, name, ok := core.QualifiedReferenceParts(ref.Query); ok {
		artist, title = a, name
	}
	query := url.Values{"release_title": {title}}
	if artist != "" {
		query.Set("artist", artist)
	}
	page, err := c.discogsSearch(ctx, query)
	if err != nil {
		return ref, false
	}
	result := core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: resolver.CatalogVersion()}
	var releases []discogsRelease
	identities := map[string]bool{}
	for _, item := range page.Results[:min(len(page.Results), discogsReleaseLimit)] {
		if item.Type != "release" {
			continue
		}
		release, err := c.discogsRelease(ctx, item.ID)
		if err != nil {
			return ref, false
		} // no identity decision from a partial lookup
		names := discogsNames(release.Artists)
		if seedNameKey(release.Title) != seedNameKey(title) || len(names) != 1 || artist != "" && !seedNameMatches(artist, names) {
			continue
		}
		identity := fmt.Sprintf("release:%d", release.ID)
		if release.MasterID > 0 {
			identity = fmt.Sprintf("master:%d", release.MasterID)
		}
		identities[identity] = true
		releases = append(releases, release)
	}
	if len(identities) == 0 {
		return ref, false
	}
	if len(identities) > 1 || len(page.Results) > discogsReleaseLimit || page.Pagination.Items > max(page.FetchedResults, len(page.Results)) {
		result.Status = core.ResolutionAmbiguous
		ref.Resolution = &result
		snapshot.Notices = append(snapshot.Notices, "Discogs album identity is ambiguous or the search is incomplete. Add the artist and exact album title.")
		return ref, true
	}
	candidate := core.ResolutionCandidate{Kind: core.ReferenceAlbum, EntityID: discogsSource(releases[0].ID), Artist: discogsNames(releases[0].Artists)[0], Title: releases[0].Title}
	seen := map[string]bool{}
	for _, release := range releases {
		source := discogsSource(release.ID)
		for _, track := range discogsCatalogTracks(ctx, release, cat, resolver, nil) {
			key := core.ProvisionalRecordingKey(track)
			if seen[key] {
				continue
			}
			seen[key] = true
			candidate.Representatives = append(candidate.Representatives, core.WeightedTrack{TrackID: track.ID})
			snapshot.Candidates = append(snapshot.Candidates, track)
		}
		candidate.Evidence = append(candidate.Evidence, core.ResolutionEvidence{Match: "source", MatchedText: source})
		snapshot.Sources = append(snapshot.Sources, source)
	}
	if len(candidate.Representatives) == 0 {
		return ref, false
	}
	for i := range candidate.Representatives {
		candidate.Representatives[i].Weight = 1 / float64(len(candidate.Representatives))
	}
	result.Status, result.Selected = core.ResolutionResolved, &candidate
	ref.TrackID, ref.Resolution = candidate.Representatives[0].TrackID, &result
	snapshot.Notices = append(snapshot.Notices, "MusicBrainz was unavailable; Discogs supplied catalog-matched album recordings. Release metadata does not verify each track's musical fit.")
	return ref, true
}
