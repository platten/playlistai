package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Recording tags propose candidates; they never establish absence of vocals.
// The normal CLAP session checks these references before they seed retrieval.
func (c *Client) discoverInstrumental(ctx context.Context, intent *core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, p ports.Progress) {
	p.Report("generation", 0, 0, "Finding instrumental recordings online")
	for offset := 0; offset < 300 && len(snapshot.Candidates) < 40; offset += 100 {
		query := url.Values{"query": {`tag:"instrumental"`}, "fmt": {"json"}, "limit": {"100"}}
		if offset > 0 {
			query.Set("offset", strconv.Itoa(offset))
		}
		path := "/ws/2/recording?" + query.Encode()
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			snapshot.Notices = append(snapshot.Notices, "The MusicBrainz instrumental search could not complete: "+err.Error())
			break
		}
		var response struct {
			Count      int           `json:"count"`
			Recordings []mbRecording `json:"recordings"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			snapshot.Notices = append(snapshot.Notices, "MusicBrainz returned an unreadable instrumental search response.")
			break
		}
		snapshot.Sources = append(snapshot.Sources, c.base+path)
		for _, recording := range response.Recordings {
			if ctx.Err() != nil {
				break
			}
			c.addKnowledgeRecording(recording, cat, resolver, snapshot)
		}
		if len(response.Recordings) < 100 || offset+100 >= response.Count {
			break
		}
	}
	if len(snapshot.Candidates) == 0 {
		c.discoverInstrumentalFallback(ctx, cat, resolver, snapshot)
	}
	if len(snapshot.Candidates) == 0 {
		snapshot.Notices = append(snapshot.Notices, "Instrumental lookup found no matching catalog recordings. Retry the search or add a specific instrumental artist or track reference.")
		return
	}
	snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("Found %d possible instrumental recordings. CLAP will screen their previews before selection.", len(snapshot.Candidates)))
	if intent.Seed.IsZero() {
		intent.Seed = core.NewRNGSeed(rand.Uint64())
	} //nolint:gosec // discovery sampling, saved for replay
	seed, _ := intent.Seed.Int64()
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // reproducible sampling, not security
	for _, index := range rng.Perm(len(snapshot.Candidates)) {
		if len(intent.InferredAnchors) >= 3 {
			break
		}
		track := snapshot.Candidates[index]
		intent.InferredAnchors = append(intent.InferredAnchors, core.InferredAnchor{
			Reference: core.IntentReference{Kind: core.ReferenceTrack, Query: track.Display(), TrackID: track.ID, Influence: core.InfluencePositive},
			Role:      "online instrumental candidate; requires CLAP screening",
		})
	}
}

func (c *Client) discoverInstrumentalFallback(ctx context.Context, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) {
	seen := map[string]bool{}
	add := func(ref core.TrackRef) {
		if !seen[ref.ID] {
			snapshot.Candidates = append(snapshot.Candidates, ref)
			seen[ref.ID] = true
		}
	}
	// A title is only a retrieval hint. It never grants vocal eligibility.
	for offset := 0; offset < 200 && len(snapshot.Candidates) < 40; offset += 100 {
		path := "/search?" + url.Values{"q": {`track:"instrumental"`}, "limit": {"100"}, "index": {strconv.Itoa(offset)}}.Encode()
		var page struct {
			Data []deezerSeedTrack `json:"data"`
			Next string            `json:"next"`
		}
		if err := c.deezerSeedGet(ctx, path, &page, snapshot); err != nil {
			break
		}
		for _, recording := range page.Data[:min(len(page.Data), 100)] {
			if ref, ok := matchSeedRecording(ctx, cat, resolver, recording.Title, []string{recording.Artist.Name}); ok {
				add(ref)
			}
		}
		if len(page.Data) == 0 || page.Next == "" {
			break
		}
	}
	if len(snapshot.Candidates) > 0 {
		snapshot.Notices = append(snapshot.Notices, "Used Deezer recording search because MusicBrainz supplied no catalog starting points.")
	}
	// Keep the same descriptive request usable during metadata outages.
	// Exact catalog titles containing the instrumental qualifier are proposals,
	// still subject to verified Deezer identity and CLAP at selection time.
	before := len(snapshot.Candidates)
	for _, ref := range cat.Resolve("instrumental", 100) {
		if strings.Contains(strings.ToLower(ref.Title), "instrumental") {
			add(ref)
		}
	}
	if len(snapshot.Candidates) > before {
		snapshot.Notices = append(snapshot.Notices, "Also checking recordings labeled instrumental in the local catalog.")
	}
}
