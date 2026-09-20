package localcatalog

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

// DiscoveryProfiles uses attributed recording tags to select artists, including
// comparable artists. Profiles are bounded samples, never recording evidence.
func (c *Catalog) DiscoveryProfiles(ctx context.Context, intent core.MusicIntent, limit int) ([]core.DiscoveryProfile, error) {
	if limit <= 0 {
		return nil, nil
	}
	limit = min(limit, 32)
	artists := map[string]string{}
	queries := recommendationQueries(ports.RetrievalRequest{Intent: intent})
	for _, ref := range intent.References {
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		if ref.Kind == core.ReferenceArtist {
			artists[normalizeUnicode(ref.Query)] = ref.Query
		}
		if c.owns(ref.TrackID) {
			if track, ok, _ := c.Lookup(ctx, ref.TrackID); ok {
				artists[normalizeUnicode(track.Artist)] = track.Artist
			}
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			if name := ref.Resolution.Selected.Artist; name != "" {
				artists[normalizeUnicode(name)] = name
			}
		}
	}
	seedNames := sortedArtistNames(artists)
	preferred := map[string]bool{}
	for _, name := range seedNames[:min(len(seedNames), 8)] {
		profile, err := c.artistProfile(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, group := range []struct {
			kind   string
			values []string
		}{{"genre", profile.Genres}, {"mood", profile.Moods}} {
			kind, values := group.kind, group.values
			for _, value := range values[:min(len(values), 3)] {
				preferred[kind+":"+normalizeUnicode(value)] = true
				criterion := core.MusicalCriterion{Kind: kind, Value: value}
				queries = append(queries, Query{Metadata: &MetadataQuery{Text: value, Criterion: &criterion, Limit: 100}})
			}
		}
	}
	// Deterministic order keeps query budgets and tie breaking reproducible.
	sort.SliceStable(queries, func(i, j int) bool {
		if queries[i].Metadata == nil {
			return false
		}
		if queries[j].Metadata == nil {
			return true
		}
		return queries[i].Metadata.Text < queries[j].Metadata.Text
	})
	for _, query := range queries[:min(len(queries), 24)] {
		if query.Metadata == nil {
			continue
		}
		hits, err := c.Search(ctx, *query.Metadata)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if len(artists) < 128 {
				artists[normalizeUnicode(hit.Track.Artist)] = hit.Track.Artist
			}
		}
	}
	var out []core.DiscoveryProfile
	for _, name := range sortedArtistNames(artists) {
		profile, err := c.artistProfile(ctx, name)
		if err != nil {
			return nil, err
		}
		if len(profile.SupportingTracks) > 0 {
			out = append(out, profile)
		}
		if len(out) < 128 {
			albums := map[string]string{}
			for _, id := range profile.SupportingTracks {
				if track, ok, _ := c.Lookup(ctx, id); ok && track.Album != "" {
					albums[track.Album] = track.Album
				}
			}
			for _, album := range sortedArtistNames(albums)[:min(len(albums), 4)] {
				albumProfile, e := c.profileForAlbum(ctx, name, album)
				if e != nil {
					return nil, e
				}
				out = append(out, albumProfile)
			}
		}
	}
	seeds := map[string]bool{}
	for _, name := range seedNames {
		seeds[normalizeUnicode(name)] = true
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Artist coverage precedes album detail so a seed's many albums cannot
		// crowd all comparable artists out of the bounded discovery budget.
		if (out[i].Album == "") != (out[j].Album == "") {
			return out[i].Album == ""
		}
		a, b := seeds[normalizeUnicode(out[i].Artist)], seeds[normalizeUnicode(out[j].Artist)]
		if a != b {
			return a
		}
		score := func(p core.DiscoveryProfile) int {
			s := 0
			for _, g := range p.Genres {
				if preferred["genre:"+normalizeUnicode(g)] {
					s++
				}
			}
			for _, m := range p.Moods {
				if preferred["mood:"+normalizeUnicode(m)] {
					s++
				}
			}
			return s
		}
		if score(out[i]) != score(out[j]) {
			return score(out[i]) > score(out[j])
		}
		if out[i].Artist != out[j].Artist {
			return out[i].Artist < out[j].Artist
		}
		return out[i].Album < out[j].Album
	})
	return out[:min(limit, len(out))], nil
}

func sortedArtistNames(names map[string]string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (c *Catalog) artistProfile(ctx context.Context, artist string) (core.DiscoveryProfile, error) {
	return c.profileForAlbum(ctx, artist, "")
}

func (c *Catalog) profileForAlbum(ctx context.Context, artist, album string) (core.DiscoveryProfile, error) {
	revision := c.provenance.MetadataGeneration
	if c.provenance.ProfileGeneration != "" {
		revision = c.provenance.ProfileGeneration
	}
	p := core.DiscoveryProfile{Artist: artist, PackIDs: []string{c.provenance.PackID}, Sources: []core.ContextSource{{Provider: c.provenance.Source, Revision: revision}}}
	p.Album = album
	refs, err := c.artistRecordings(ctx, artist, 256)
	if err != nil {
		return p, err
	}
	genres, moods := map[string]int{}, map[string]int{}
	seen := map[string]bool{}
	artistIDs := map[string]bool{}
	for _, ref := range refs[:min(len(refs), 256)] {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		track, ok, err := c.Lookup(ctx, ref.ID)
		if err != nil {
			return p, err
		}
		if !ok {
			continue
		}
		if album != "" && track.Album != album {
			continue
		}
		identity := track.RecordingIdentity
		if identity == "" {
			identity = normalizeUnicode(track.Artist + "\x00" + track.Title)
		}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if len(p.SupportingTracks) == 0 {
			p.Artist = track.Artist
		}
		p.SupportingTracks = append(p.SupportingTracks, ref.ID)
		for _, annotation := range c.Annotations(ctx, ref.ID) {
			switch annotation.Kind {
			case "artist_mbid":
				if id := librarypack.CanonicalMusicBrainzRecordingID(annotation.Value); id != "" {
					artistIDs[id] = true
				}
			case "genre", "style":
				genres[annotation.Value]++
			case "mood":
				moods[annotation.Value]++
			case "original_release_date":
				value := strings.TrimSpace(annotation.Value)
				if len(value) < 4 {
					continue
				}
				year, e := strconv.Atoi(value[:4])
				if e != nil || year < 1000 || year > 9999 {
					continue
				}
				if p.FirstYear == 0 || year < p.FirstYear {
					p.FirstYear = year
				}
				p.LastYear = max(p.LastYear, year)
			}
		}
	}
	p.Genres = frequentValues(genres)
	p.Moods = frequentValues(moods)
	if len(artistIDs) == 1 {
		for id := range artistIDs {
			p.ArtistID = id
		}
	}
	if c.profiles != nil {
		// The companion records balanced distributions built at release time;
		// its digest/pack binding was checked before this catalog was opened.
		rows, e := c.profiles.QueryContext(ctx, "SELECT kind,value,CASE WHEN album='' THEN albums ELSE recordings END FROM profiles WHERE pack_id=? AND artist=? AND album=? AND period='' ORDER BY kind,value", c.provenance.PackID, p.Artist, album)
		if e != nil {
			return p, e
		}
		genres, moods = map[string]int{}, map[string]int{}
		for rows.Next() {
			var kind, value string
			var count int
			if e := rows.Scan(&kind, &value, &count); e != nil {
				_ = rows.Close()
				return p, e
			}
			switch kind {
			case "genre", "style":
				genres[value] += count
			case "mood":
				moods[value] += count
			}
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return p, e
		}
		p.Genres = frequentValues(genres)
		p.Moods = frequentValues(moods)
	}
	return p, nil
}

func frequentValues(counts map[string]int) []string {
	values := make([]string, 0, len(counts))
	for value := range counts {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if counts[values[i]] != counts[values[j]] {
			return counts[values[i]] > counts[values[j]]
		}
		return values[i] < values[j]
	})
	return values[:min(len(values), 8)]
}

func (c *CompositeCatalog) DiscoveryProfiles(ctx context.Context, intent core.MusicIntent, limit int) ([]core.DiscoveryProfile, error) {
	profiles, err := c.local.DiscoveryProfiles(ctx, intent, limit)
	if err != nil {
		return nil, err
	}
	if base, ok := c.base.(ports.DiscoveryProfileCatalog); ok {
		more, e := base.DiscoveryProfiles(ctx, intent, limit)
		if e != nil {
			return nil, e
		}
		// Preserve coverage of both pins; a large personal library must not
		// consume the entire budget before the shared corpus is considered.
		balanced := make([]core.DiscoveryProfile, 0, len(profiles)+len(more))
		for i := 0; i < max(len(profiles), len(more)); i++ {
			if i < len(profiles) {
				balanced = append(balanced, profiles[i])
			}
			if i < len(more) {
				balanced = append(balanced, more[i])
			}
		}
		profiles = balanced
	}
	if limit <= 0 {
		return nil, nil
	}
	return profiles[:min(limit, len(profiles))], nil
}
