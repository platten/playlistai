package musicbrainz

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func (c *Client) contextArtistAlbums(ctx context.Context, artistID, artist string, period *core.TemporalRequirement, early bool, cat ports.Catalog, resolver ports.ReferenceResolver, plan *core.ContextSeedPlan) {
	// Filter at the release-group level before pagination. Filtering recording
	// results this way would incorrectly remove originals also on compilations.
	query := "arid:" + artistID + " AND primarytype:album AND NOT secondarytype:* AND status:official"
	path := "/ws/2/release-group?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		plan.Profile.ScopeNote = "Album or era scope could not be established; no contextual seeds were substituted."
		return
	}
	var page struct {
		Count  int             `json:"count"`
		Groups []contextEntity `json:"release-groups"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return
	}
	plan.Profile.Sources = append(plan.Profile.Sources, contextMBSource(c.base+path, raw))
	// A truncated response cannot establish which albums were earliest.
	if early && page.Count > len(page.Groups) {
		plan.Profile.ScopeNote = "The discography response was incomplete; early-album seeds remain unavailable."
		return
	}
	var groups []contextEntity
	for _, group := range page.Groups {
		year := yearOf(group.FirstDate)
		if !contextMBID.MatchString(group.ID) || year == 0 || group.PrimaryType != "Album" || len(group.Secondary) > 0 || !contextArtistCredit(group.ArtistCredit, artistID, artist) {
			continue
		}
		if period != nil && (year < period.StartYear || year > period.EndYear) {
			continue
		}
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].FirstDate == groups[j].FirstDate {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].FirstDate < groups[j].FirstDate
	})
	for _, group := range groups[:min(len(groups), 2)] {
		plan.Profile.ReleaseGroupIDs = append(plan.Profile.ReleaseGroupIDs, group.ID)
		year := yearOf(group.FirstDate)
		if plan.Profile.FirstYear == 0 {
			plan.Profile.FirstYear = year
		}
		plan.Profile.LastYear = year
		seeds := c.contextRecordingSeeds(ctx, "rgid:"+group.ID, artistID, artist, cat, resolver, &plan.Profile)
		// Keep room for each album, so the first album cannot consume every seed.
		plan.Seeds = append(plan.Seeds, seeds[:min(len(seeds), 2)]...)
	}
	plan.Seeds = contextCatalogSeeds(plan.Seeds, cat)
	if early {
		plan.Profile.ScopeNote = "Early sound is proposed from the first two dated studio albums in the complete returned discography; this is a retrieval interpretation, not an output date requirement."
	} else {
		plan.Profile.ScopeNote = "Seeds come from up to two studio albums whose original dates match the requested period; musical suitability still requires recording evidence."
	}
	if len(plan.Seeds) == 0 {
		plan.Profile.ScopeNote += " No unambiguous catalog recording seeds were available for that scope."
	}
}

func (c *Client) contextRecordingSeeds(ctx context.Context, query, artistID, artist string, cat ports.Catalog, resolver ports.ReferenceResolver, profile *core.ContextProfile) []core.WeightedTrack {
	path := "/ws/2/recording?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return nil
	}
	var page struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return nil
	}
	profile.Sources = append(profile.Sources, contextMBSource(c.base+path, raw))
	// Reuse exact recording matching and ambiguity detection in an isolated
	// snapshot. No artist/album genres are written into recording evidence.
	var recordings core.KnowledgeSnapshot
	for _, recording := range page.Recordings[:min(len(page.Recordings), 100)] {
		if ctx.Err() != nil {
			break
		}
		if !contextArtistCredit(recording.ArtistCredit, artistID, artist) {
			continue
		}
		for i := range recording.ArtistCredit {
			if recording.ArtistCredit[i].Name == "" {
				recording.ArtistCredit[i].Name = recording.ArtistCredit[i].Artist.Name
			}
		}
		c.addKnowledgeRecording(recording, cat, resolver, &recordings)
	}
	sort.Slice(recordings.Tracks, func(i, j int) bool { return recordings.Tracks[i].Ref.ID < recordings.Tracks[j].Ref.ID })
	var seeds []core.WeightedTrack
	seen := map[string]bool{}
	for _, recording := range recordings.Tracks {
		key := core.ProvisionalRecordingKey(recording.Ref)
		if recording.IdentityStatus != core.ResolutionResolved || seen[key] {
			continue
		}
		seen[key] = true
		seeds = append(seeds, core.WeightedTrack{TrackID: recording.Ref.ID, Weight: 1})
	}
	return contextDiverseSeeds(ctx, seeds, cat)
}

// Select a central recording, then the farthest uncovered recordings. The
// distances use the shipped catalog's audio space only, never a CLAP/MERT vector
// or text embedding. Diversity does not certify the requested musical traits.
func contextDiverseSeeds(ctx context.Context, seeds []core.WeightedTrack, cat ports.Catalog) []core.WeightedTrack {
	var valid []core.WeightedTrack
	var vectors [][]float32
	for _, seed := range seeds[:min(len(seeds), 100)] {
		v, ok := cat.Vectors(seed.TrackID)
		if !ok || len(v.Audio) != cat.Dim() || cat.Dim() == 0 {
			continue
		}
		var norm float64
		for _, value := range v.Audio {
			norm += float64(value) * float64(value)
		}
		if norm <= 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			continue
		}
		valid = append(valid, seed)
		vectors = append(vectors, v.Audio)
	}
	if len(valid) == 0 {
		return contextCatalogSeeds(seeds, cat)
	}
	distance := func(i, j int) float64 {
		var dot, a, b float64
		for k, value := range vectors[i] {
			x, y := float64(value), float64(vectors[j][k])
			dot += x * y
			a += x * x
			b += y * y
		}
		return 1 - math.Max(-1, math.Min(1, dot/math.Sqrt(a*b)))
	}
	first, lowest := 0, math.Inf(1)
	for i := range valid {
		if ctx.Err() != nil {
			return nil
		}
		var cost float64
		for j := range valid {
			cost += distance(i, j)
		}
		if cost < lowest {
			first, lowest = i, cost
		}
	}
	chosen := []int{first}
	used := map[int]bool{first: true}
	for len(chosen) < min(contextSeeds, len(valid)) {
		best, farthest := -1, -1.0
		for i := range valid {
			if used[i] {
				continue
			}
			nearest := math.Inf(1)
			for _, prior := range chosen {
				nearest = math.Min(nearest, distance(i, prior))
			}
			if nearest > farthest {
				best, farthest = i, nearest
			}
		}
		chosen = append(chosen, best)
		used[best] = true
	}
	var selected []core.WeightedTrack
	for _, index := range chosen {
		selected = append(selected, core.WeightedTrack{TrackID: valid[index].TrackID, Weight: 1})
	}
	return contextCatalogSeeds(selected, cat)
}

func (c *Client) genreContext(ctx context.Context, intent core.MusicIntent, snapshot *core.KnowledgeSnapshot) {
	genres := discoveryGenres(intent)
	if len(genres) == 0 {
		return
	}
	graph, err := c.Graph(ctx, genres[:min(len(genres), 2)])
	if err != nil {
		return
	}
	for _, genre := range genres[:min(len(genres), 2)] {
		id := graph.ID(genre)
		for _, node := range graph.Nodes {
			if node.ID != id || strings.HasPrefix(id, "discogs-dump:") || !contextMBID.MatchString(id) {
				continue
			}
			profile := core.ContextProfile{Kind: "genre", EntityKey: "musicbrainz:genre:" + id, Query: genre, Name: node.Name, Genres: []string{node.Name}, ExtractorVersion: core.ContextProfileVersion,
				Sources:   []core.ContextSource{{Provider: "musicbrainz", URL: c.base + "/genre/" + id, Revision: graph.Version, License: "CC-BY-NC-SA-3.0"}},
				ScopeNote: "Genre identity and aliases guide discovery; related genres are not equivalent recording classifications."}
			profile.ID = knowledgeHash(profile)
			snapshot.ContextPlans = append(snapshot.ContextPlans, core.ContextSeedPlan{Query: genre, EntityKey: profile.EntityKey, Scope: "playlist", Profile: profile})
			break
		}
	}
}
