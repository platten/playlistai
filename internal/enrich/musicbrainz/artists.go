package musicbrainz

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/url"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const genreArtistTarget = 100
const maxSampledArtists = 6
const recordingsPerArtist = 3

func (c *Client) genreArtists(ctx context.Context, genre string) core.GenreArtistPool {
	query := `tag:"` + mbEscape(genre) + `"`
	pool := c.genreArtistsQuery(ctx, genre, query)
	// A compound description may be represented by separate artist tags.
	// Require every term, never drop a trailing word or infer recording genre.
	terms := strings.Fields(genre)
	if len(pool.Artists) < genreArtistTarget && pool.Complete && len(terms) > 1 && len(terms) <= 4 {
		var clauses []string
		for _, term := range terms {
			clauses = append(clauses, `tag:"`+mbEscape(term)+`"`)
		}
		fallback := c.genreArtistsQuery(ctx, genre, strings.Join(clauses, " AND "))
		seen := map[string]bool{}
		for _, artist := range pool.Artists {
			seen[artist.ID] = true
		}
		for _, artist := range fallback.Artists {
			if !seen[artist.ID] && len(pool.Artists) < genreArtistTarget {
				pool.Artists = append(pool.Artists, artist)
				seen[artist.ID] = true
			}
		}
		pool.Sources = append(pool.Sources, fallback.Sources...)
		pool.Available = max(pool.Available, fallback.Available)
		pool.Complete = pool.Complete && fallback.Complete && len(pool.Artists) >= pool.Available
	}
	return pool
}

func (c *Client) genreArtistsQuery(ctx context.Context, genre, query string) core.GenreArtistPool {
	pool := core.GenreArtistPool{Genre: genre}
	seen := map[string]bool{}
	for offset := 0; len(pool.Artists) < genreArtistTarget; {
		path := "/ws/2/artist?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}, "offset": {fmt.Sprint(offset)}}.Encode()
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			break
		}
		var page struct {
			Count   int `json:"count"`
			Artists []struct {
				ID   string  `json:"id"`
				Name string  `json:"name"`
				Tags []mbTag `json:"tags"`
			} `json:"artists"`
		}
		if json.Unmarshal(raw, &page) != nil {
			break
		}
		pool.Sources = append(pool.Sources, c.base+path)
		pool.Available = page.Count
		for _, artist := range page.Artists {
			if artist.ID == "" || artist.Name == "" || seen[artist.ID] {
				continue
			}
			seen[artist.ID] = true
			entry := core.GenreArtist{ID: artist.ID, Name: artist.Name}
			for _, tag := range artist.Tags {
				entry.Tags = append(entry.Tags, core.AttributedGenreTag{Name: tag.Name, Votes: tag.Count, Source: "musicbrainz", EntityID: artist.ID, Facet: "artist_tag"})
			}
			pool.Artists = append(pool.Artists, entry)
		}
		offset += len(page.Artists)
		if offset >= page.Count || len(page.Artists) == 0 {
			pool.Complete = true
			break
		}
	}
	return pool
}

func (c *Client) sampleGenreArtists(ctx context.Context, intent *core.MusicIntent, genres []string, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) error {
	if intent.Seed.IsZero() {
		var bits [8]byte
		if _, err := rand.Read(bits[:]); err != nil {
			return err
		}
		intent.Seed = core.NewRNGSeed(binary.LittleEndian.Uint64(bits[:]) | 1)
	}
	seed, err := intent.Seed.Int64()
	if err != nil {
		return err
	}
	rng := mathrand.New(mathrand.NewSource(seed))
	excluded := intent.Normalized().Constraints.ArtistsExclude
	seen := map[string]bool{}
	// Acquire the broad pools before spending the budget on individual artists.
	for _, genre := range genres {
		key := core.NormalizeIdentityPart(genre)
		if seen[key] {
			continue
		}
		seen[key] = true
		pool := c.genreArtists(ctx, genre)
		if len(pool.Artists) < genreArtistTarget {
			snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Found %d of the target 100 artists for %q; provider coverage or the metadata budget limited this pool.", len(pool.Artists), genre))
		}
		snapshot.Sources = append(snapshot.Sources, pool.Sources...)
		snapshot.ArtistPools = append(snapshot.ArtistPools, pool)
	}
	orders := make([][]int, len(snapshot.ArtistPools))
	positions := make([]int, len(orders))
	for i, pool := range snapshot.ArtistPools {
		orders[i] = rng.Perm(len(pool.Artists))
	}
	used := map[string]bool{}
	attempts := 0
	for round := 0; round < 3 && attempts < maxSampledArtists; round++ {
		for i := range snapshot.ArtistPools {
			pool := &snapshot.ArtistPools[i]
			for positions[i] < len(orders[i]) && attempts < maxSampledArtists {
				if ctx.Err() != nil {
					return nil
				}
				artist := pool.Artists[orders[i][positions[i]]]
				positions[i]++
				if used[artist.ID] || excludedArtist(artist.Name, excluded) {
					continue
				}
				resolution := resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: artist.Name})
				if resolution.Status != core.ResolutionResolved || resolution.Selected == nil || core.NormalizeIdentityPart(resolution.Selected.Artist) != core.NormalizeIdentityPart(artist.Name) {
					continue
				}
				used[artist.ID] = true
				attempts++
				pool.SampledArtists = append(pool.SampledArtists, artist.ID)
				c.sampleArtistRecordings(ctx, artist, excluded, rng, cat, resolver, snapshot, pool)
				break
			}
		}
	}
	// ResolveMusic distinguishes its bounded enrichment deadline from caller
	// cancellation and returns whatever evidence was collected on timeout.
	return nil
}

func excludedArtist(name string, excluded []string) bool {
	for _, value := range excluded {
		if core.NormalizeIdentityPart(name) == core.NormalizeIdentityPart(value) {
			return true
		}
	}
	return false
}

func (c *Client) sampleArtistRecordings(ctx context.Context, artist core.GenreArtist, excluded []string, rng *mathrand.Rand, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, pool *core.GenreArtistPool) {
	path := "/ws/2/recording?" + url.Values{"query": {"arid:" + artist.ID}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return
	}
	var page struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return
	}
	snapshot.Sources = append(snapshot.Sources, c.base+path)
	added := 0
	for _, index := range rng.Perm(len(page.Recordings)) {
		if ctx.Err() != nil {
			break
		}
		recording := page.Recordings[index]
		credited, blocked := false, false
		for _, credit := range recording.ArtistCredit {
			credited = credited || credit.Artist.ID == artist.ID
			blocked = blocked || excludedArtist(credit.Name, excluded)
		}
		if !credited || blocked || strings.TrimSpace(recording.ID) == "" {
			continue
		}
		before := len(snapshot.Candidates)
		c.addKnowledgeRecording(recording, cat, resolver, snapshot)
		if len(snapshot.Candidates) > before {
			pool.SampledTracks = append(pool.SampledTracks, snapshot.Candidates[len(snapshot.Candidates)-1].ID)
			added++
			if added >= recordingsPerArtist {
				break
			}
		}
	}
}
