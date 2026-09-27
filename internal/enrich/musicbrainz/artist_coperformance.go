package musicbrainz

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

type artistCoperformanceDocument struct {
	ID        string `json:"id"`
	Relations []struct {
		Type       string      `json:"type"`
		Direction  string      `json:"direction"`
		TargetType string      `json:"target-type"`
		Recording  mbRecording `json:"recording"`
	} `json:"relations"`
}

type artistCoperformanceResponse struct {
	anchor string
	source core.ContextSource
	// artist MBID -> distinct recordings on which the anchor performed.
	credits map[string]map[string]bool
}

// CorroborateArtistReferences is the bounded identity-only step used on an
// explicit submission. It leaves Knowledge unset so generation still prepares
// its catalog seeds and recording evidence.
func (c *Client) CorroborateArtistReferences(ctx context.Context, intent core.MusicIntent) (core.MusicIntent, error) {
	if err := ctx.Err(); err != nil {
		return intent, err
	}
	intent = c.corroborateArtistReferences(ctx, intent, &core.KnowledgeSnapshot{})
	return intent, ctx.Err()
}

// Corroboration only connects independently recognized, explicit playlist
// references. It does not turn artist collaboration into recording suitability.
func (c *Client) corroborateArtistReferences(ctx context.Context, intent core.MusicIntent, snapshot *core.KnowledgeSnapshot) core.MusicIntent {
	var targets []int
	anchors := map[string]bool{}
	for i, ref := range intent.References {
		if !coperformanceReference(intent, ref) || ref.Grounding == nil || ref.Grounding.Truncated {
			continue
		}
		g := ref.Grounding
		if len(g.Candidates) == 1 && ref.Resolution != nil && ref.Resolution.Status == core.ResolutionResolved && ref.Resolution.Selected != nil {
			candidate := g.Candidates[0]
			if candidate.Kind == core.ReferenceArtist && candidate.Name != "" && g.Corroboration == nil {
				if id := librarypack.CanonicalMusicBrainzRecordingID(candidate.ID); id != "" {
					anchors[id] = true
				}
			}
		} else if len(g.Candidates) >= 2 && len(g.Candidates) <= 64 && !g.Confirmed && g.Corroboration == nil && ref.TrackID == "" && ref.SpellingDecision == "" {
			targets = append(targets, i)
		}
	}
	// A third independently named anchor could identify a competing namesake.
	// Do not silently truncate the user's disambiguating context.
	if len(targets) == 0 || len(anchors) == 0 || len(anchors) > 2 || ctx.Err() != nil {
		return intent
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	if budget == nil {
		budget = &knowledgeBudget{}
		ctx = context.WithValue(ctx, knowledgeBudgetKey{}, budget)
	}
	budget.mu.Lock()
	limit := min(KnowledgeRequests, budget.requests+2)
	budget.mu.Unlock()
	if inherited, ok := ctx.Value(contextRequestLimitKey{}).(int); ok {
		limit = min(limit, inherited)
	}
	ctx = context.WithValue(ctx, contextRequestLimitKey{}, limit)
	anchorIDs := make([]string, 0, len(anchors))
	for id := range anchors {
		anchorIDs = append(anchorIDs, id)
	}
	sort.Strings(anchorIDs)
	var responses []artistCoperformanceResponse
	for _, id := range anchorIDs {
		response, ok := c.artistCoperformance(ctx, id)
		if !ok {
			return intent
		}
		responses = append(responses, response)
	}
	refs := append([]core.IntentReference(nil), intent.References...)
	for _, index := range targets {
		g := *refs[index].Grounding
		selected := ""
		conflict := false
		for _, candidate := range g.Candidates {
			id := strings.ToLower(candidate.ID)
			for _, response := range responses {
				if len(response.credits[id]) > 0 {
					if selected != "" && selected != id {
						conflict = true
						break
					}
					selected = id
				}
			}
			if conflict {
				break
			}
		}
		if selected == "" || conflict {
			continue
		}
		proof := &core.IdentityCorroboration{SelectedID: selected, Method: core.ArtistCoperformanceMethod}
		for _, response := range responses {
			if len(response.credits[selected]) == 0 {
				continue
			}
			ids := make([]string, 0, len(response.credits[selected]))
			for id := range response.credits[selected] {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			proof.Supports = append(proof.Supports, core.IdentityCorroborationSupport{AnchorID: response.anchor, RecordingIDs: ids, Source: response.source})
		}
		g.Corroboration = proof
		if _, ok := g.CorroboratedArtist(); !ok || ctx.Err() != nil {
			continue
		}
		g.Candidates = append([]core.IdentityCandidate(nil), g.Candidates...)
		refs[index].Grounding = &g
		snapshot.Notices = append(snapshot.Notices, "Corroborated the artist identity for "+refs[index].Query+" using recording performance credits shared with another requested artist; a catalog seed is still required.")
		for _, response := range responses {
			snapshot.Sources = append(snapshot.Sources, response.source.URL)
		}
	}
	intent.References = refs
	return intent
}

func coperformanceReference(intent core.MusicIntent, ref core.IntentReference) bool {
	if ref.Kind != core.ReferenceArtist || ref.Influence != core.InfluencePositive || ref.SpellingDecision != "" {
		return false
	}
	explicit := false
	for _, evidence := range ref.Evidence {
		explicit = explicit || evidence.Explicit
	}
	if !explicit {
		return false
	}
	key := core.NormalizeIdentityPart(ref.Query)
	for _, endpoint := range []*core.IntentReference{intent.Start, intent.Destination} {
		if endpoint != nil && core.NormalizeIdentityPart(endpoint.Query) == key {
			return false
		}
	}
	for _, group := range [][]core.IntentReference{intent.Journey.Waypoints, intent.RequiredTracks} {
		for _, other := range group {
			if core.NormalizeIdentityPart(other.Query) == key {
				return false
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind == "require_artist" && core.NormalizeIdentityPart(constraint.Value) == key {
			return false
		}
	}
	return true
}

func (c *Client) artistCoperformance(ctx context.Context, anchor string) (artistCoperformanceResponse, bool) {
	path := "/ws/2/artist/" + anchor + "?" + url.Values{"fmt": {"json"}, "inc": {"recording-rels+artist-credits"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	var document artistCoperformanceDocument
	if err != nil || json.Unmarshal(raw, &document) != nil || !strings.EqualFold(document.ID, anchor) || document.Relations == nil || len(document.Relations) > 1024 {
		return artistCoperformanceResponse{}, false
	}
	response := artistCoperformanceResponse{anchor: anchor, source: core.ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org" + path, Revision: knowledgeHash(json.RawMessage(raw)), License: "CC0-1.0"}, credits: map[string]map[string]bool{}}
	for _, relation := range document.Relations {
		if ctx.Err() != nil {
			return artistCoperformanceResponse{}, false
		}
		if relation.Type != "instrument" && relation.Type != "vocal" && relation.Type != "performer" {
			continue
		}
		if relation.TargetType != "recording" || relation.Direction != "forward" && relation.Direction != "backward" {
			return artistCoperformanceResponse{}, false
		}
		if relation.Direction != "forward" {
			continue
		}
		recordingID := librarypack.CanonicalMusicBrainzRecordingID(relation.Recording.ID)
		if recordingID == "" || len(relation.Recording.ArtistCredit) == 0 {
			return artistCoperformanceResponse{}, false
		}
		for _, credit := range relation.Recording.ArtistCredit {
			artistID := librarypack.CanonicalMusicBrainzRecordingID(credit.Artist.ID)
			if artistID == "" {
				return artistCoperformanceResponse{}, false
			}
			if response.credits[artistID] == nil {
				response.credits[artistID] = map[string]bool{}
			}
			response.credits[artistID][recordingID] = true
		}
	}
	return response, true
}
