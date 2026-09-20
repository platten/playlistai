package musicbrainz

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

// packDiscoveryArtists expands locally learned artist patterns only during a
// submitted generation. Source identities, not similar names or mood labels,
// determine which artist's recordings may enter the discovery stream.
func (s *candidateStream) packDiscoveryArtists(ctx context.Context) []core.GenreArtist {
	ctx, cancel := context.WithTimeout(ctx, contextBudget)
	defer cancel()
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	if budget == nil {
		budget = &knowledgeBudget{}
		ctx = context.WithValue(ctx, knowledgeBudgetKey{}, budget)
	}
	ctx = context.WithValue(ctx, contextRequestLimitKey{}, budget.requests+contextRequests)
	var result []core.GenreArtist
	seen := map[string]bool{}
	appendArtist := func(artist core.GenreArtist) {
		if !contextMBID.MatchString(artist.ID) || artist.Name == "" || seen[artist.ID] || excludedArtist(artist.Name, s.intent.Constraints.ArtistsExclude) {
			return
		}
		seen[artist.ID] = true
		result = append(result, artist)
	}
	profileArtists := map[string]bool{}
	for _, profile := range s.snapshot.PackProfiles[:min(len(s.snapshot.PackProfiles), 12)] {
		if len(profileArtists) >= 4 {
			break
		}
		nameKey := core.NormalizeIdentityPart(profile.Artist)
		if profileArtists[nameKey] {
			continue
		}
		profileArtists[nameKey] = true
		if ctx.Err() != nil {
			break
		}
		if excludedArtist(profile.Artist, s.intent.Constraints.ArtistsExclude) {
			continue
		}
		selected := core.ResolutionCandidate{Artist: profile.Artist}
		for _, id := range profile.SupportingTracks[:min(len(profile.SupportingTracks), 4)] {
			selected.Representatives = append(selected.Representatives, core.WeightedTrack{TrackID: id, Weight: 1})
		}
		artist, sources, ok := s.client.contextArtistIdentity(ctx, selected, s.cat)
		if !ok || profile.ArtistID != "" && !strings.EqualFold(profile.ArtistID, artist.ID) {
			continue
		}
		appendArtist(core.GenreArtist{ID: artist.ID, Name: artist.Name})
		for _, source := range sources {
			s.snapshot.Sources = append(s.snapshot.Sources, source.URL)
		}
		// Explicit genres retain discovery priority. Related artists remain
		// proposals and each recording still passes the ordinary musical checks.
		for _, related := range s.client.wikidataArtistNeighbors(ctx, artist.ID, artist.Name, &s.snapshot) {
			appendArtist(related)
		}
	}
	return result
}

// wikidataArtistNeighbors follows a small number of typed influence edges. It
// never treats influence as genre equivalence or propagates artist tags onto
// recordings. Both ends must agree with their MusicBrainz identity.
func (c *Client) wikidataArtistNeighbors(ctx context.Context, id, name string, snapshot *core.KnowledgeSnapshot) []core.GenreArtist {
	path := "/ws/2/artist/" + id + "?" + url.Values{"inc": {"url-rels"}, "fmt": {"json"}}.Encode()
	raw, err := c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
	if err != nil {
		return nil
	}
	var entity contextEntity
	if json.Unmarshal(raw, &entity) != nil || !strings.EqualFold(entity.ID, id) || !seedNameMatches(name, []string{entity.Name}) {
		return nil
	}
	qid := linkedWikidata(entity)
	if qid == "" {
		return nil
	}
	item, source, ok := c.discoveryWikidataEntity(ctx, qid)
	if !ok || !wikidataIdentity(item, "P434", id) {
		return nil
	}
	snapshot.Sources = append(snapshot.Sources, source)
	var result []core.GenreArtist
	seen := map[string]bool{}
	for _, claim := range item.Claims["P737"] {
		if len(result) >= 3 || ctx.Err() != nil {
			break
		}
		if claim.Rank == "deprecated" || claim.MainSnak.SnakType != "value" {
			continue
		}
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(claim.MainSnak.DataValue.Value, &value) != nil || !contextQID.MatchString(value.ID) || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		neighbor, neighborSource, found := c.discoveryWikidataEntity(ctx, value.ID)
		if !found {
			continue
		}
		mbid := ""
		for _, linked := range neighbor.Claims["P434"] {
			if linked.Rank == "deprecated" || linked.MainSnak.SnakType != "value" {
				continue
			}
			var candidate string
			if json.Unmarshal(linked.MainSnak.DataValue.Value, &candidate) != nil || !contextMBID.MatchString(candidate) {
				continue
			}
			if mbid != "" && mbid != candidate {
				mbid = ""
				break
			}
			mbid = candidate
		}
		if mbid == "" || mbid == id {
			continue
		}
		path = "/ws/2/artist/" + mbid + "?" + url.Values{"inc": {"url-rels"}, "fmt": {"json"}}.Encode()
		raw, err = c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
		var target contextEntity
		if err != nil || json.Unmarshal(raw, &target) != nil || target.ID != mbid || target.Name == "" || linkedWikidata(target) != value.ID {
			continue
		}
		result = append(result, core.GenreArtist{ID: mbid, Name: target.Name})
		snapshot.Sources = append(snapshot.Sources, neighborSource, c.base+path)
	}
	return result
}

func (c *Client) discoveryWikidataEntity(ctx context.Context, qid string) (wikidataEntity, string, bool) {
	if !contextQID.MatchString(qid) {
		return wikidataEntity{}, "", false
	}
	path := "/wiki/Special:EntityData/" + qid + ".json"
	raw, err := c.metadataGet(ctx, c.wikidataBase, path, "wikidata-context-v1:", c.contextClient, false)
	var response struct {
		Entities map[string]wikidataEntity `json:"entities"`
	}
	if err != nil || json.Unmarshal(raw, &response) != nil {
		return wikidataEntity{}, "", false
	}
	item, ok := response.Entities[qid]
	if !ok || item.ID != qid || item.LastRevision <= 0 {
		return wikidataEntity{}, "", false
	}
	return item, "https://www.wikidata.org/wiki/Special:EntityData/" + qid + ".json?revision=" + jsonNumber(item.LastRevision), true
}

func jsonNumber(n int64) string { raw, _ := json.Marshal(n); return string(raw) }

func wikidataIdentity(item wikidataEntity, property, want string) bool {
	found := false
	for _, claim := range item.Claims[property] {
		if claim.Rank == "deprecated" || claim.MainSnak.SnakType != "value" {
			continue
		}
		var id string
		if json.Unmarshal(claim.MainSnak.DataValue.Value, &id) != nil || !strings.EqualFold(id, want) {
			return false
		}
		found = true
	}
	return found
}

func recordingNameKey(artist, title string) string {
	return "name:" + core.NormalizeIdentityPart(artist) + "\x00" + core.NormalizeIdentityPart(title)
}

// Maintain separate authoritative and provisional lookup keys. Source-prefixed
// pack IDs must not prevent provider evidence attaching to an existing track.
func indexKnownArtistRecordings(cat ports.Catalog, tracks []core.TrackRef) map[string]string {
	index := map[string]string{}
	for _, ref := range tracks {
		meta, ok := cat.Meta(ref.ID)
		if !ok {
			continue
		}
		name := recordingNameKey(ref.Artist, ref.Title)
		if prior, exists := index[name]; !exists {
			index[name] = ref.ID
		} else if prior != ref.ID {
			index[name] = ""
		}
		for _, key := range []string{"mbid:" + strings.ToLower(meta.MusicBrainzRecording), "isrc:" + strings.ToUpper(meta.ISRC)} {
			if strings.HasSuffix(key, ":") {
				continue
			}
			if prior := index[key]; prior == "" || ref.ID < prior {
				index[key] = ref.ID
			}
		}
	}
	return index
}

func matchKnownRecording(cat ports.Catalog, index map[string]string, r mbRecording) string {
	keys := []string{"mbid:" + strings.ToLower(r.ID)}
	for _, isrc := range r.ISRCs {
		keys = append(keys, "isrc:"+strings.ToUpper(isrc))
	}
	for _, credit := range r.ArtistCredit {
		name := credit.Name
		if name == "" {
			name = credit.Artist.Name
		}
		keys = append(keys, recordingNameKey(name, r.Title))
	}
	for _, key := range keys {
		id := index[key]
		if id == "" {
			continue
		}
		meta, ok := cat.Meta(id)
		if !ok {
			continue
		}
		if meta.MusicBrainzRecording != "" && r.ID != "" && !strings.EqualFold(meta.MusicBrainzRecording, r.ID) {
			continue
		}
		// Names alone cannot override contradictory recording identifiers.
		if strings.HasPrefix(key, "name:") {
			if known := librarypack.CanonicalISRC(meta.ISRC); known != "" {
				observed, matching := false, false
				for _, candidate := range r.ISRCs {
					if value := librarypack.CanonicalISRC(candidate); value != "" {
						observed = true
						matching = matching || value == known
					}
				}
				if observed && !matching {
					continue
				}
			}
		}
		return id
	}
	return ""
}
