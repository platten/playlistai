package musicbrainz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const KnowledgeBudget = 30 * time.Second
const KnowledgeRequests = 20

type knowledgeBudgetKey struct{}
type cacheOnlyKey struct{}
type knowledgeBudget struct {
	requests int
	backoff  time.Time
}

// knowledgeGet caches exact provider responses, not prompts. Stale cache is
// usable offline. Only valid responses enter the cache; outages aren't misses.
func (c *Client) knowledgeGet(ctx context.Context, path string, negative bool) ([]byte, error) {
	key := "knowledge-v1:" + path
	var cached string
	var fetched int64
	if c.db != nil {
		_ = c.db.QueryRowContext(ctx, "SELECT json,fetched_at FROM mb_cache WHERE key=?", key).Scan(&cached, &fetched)
	}
	ttl := 30 * 24 * time.Hour
	if negative || negativeKnowledge(cached) {
		ttl = 24 * time.Hour
	}
	if cached != "" && time.Since(time.Unix(fetched, 0)) < ttl {
		return []byte(cached), nil
	}
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	fallback := func(err error) ([]byte, error) {
		if cached != "" {
			return []byte(cached), nil
		}
		return nil, err
	}
	if only, _ := ctx.Value(cacheOnlyKey{}).(bool); only {
		return fallback(core.ErrUnavailable)
	}
	if budget != nil {
		if budget.requests >= KnowledgeRequests {
			return fallback(fmt.Errorf("metadata request budget exhausted"))
		}
		if delay := time.Until(budget.backoff); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return fallback(ctx.Err())
			case <-timer.C:
			}
		}
		budget.requests++
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fallback(err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json,text/html")
	req.Header.Set("Accept-Language", "en")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fallback(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if budget != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
			wait := 5 * time.Second
			if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil {
				wait = time.Duration(max(0, seconds)) * time.Second
			} else if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
				wait = time.Until(at)
			}
			budget.backoff = time.Now().Add(wait)
		}
		return fallback(fmt.Errorf("music metadata HTTP %d", resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return fallback(fmt.Errorf("invalid metadata response"))
	}
	if path != "/genres" && !strings.HasPrefix(path, "/genre/") && !json.Valid(raw) {
		return fallback(fmt.Errorf("invalid metadata JSON"))
	}
	if c.db != nil {
		_, _ = c.db.ExecContext(ctx, "INSERT OR REPLACE INTO mb_cache(key,json,fetched_at) VALUES(?,?,?)", key, string(raw), time.Now().Unix())
	}
	return raw, nil
}

func (c *Client) IsCachedGenre(ctx context.Context, name string) bool {
	graph, err := c.Graph(context.WithValue(ctx, cacheOnlyKey{}, true), []string{name})
	if err != nil {
		return false
	}
	id := graph.ID(name)
	for _, node := range graph.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

// Graph loads the provider's small genre-name index, then only the requested
// genre relationships. Names not present in MusicBrainz remain valid input.
func (c *Client) Graph(ctx context.Context, names []string) (core.GenreGraph, error) {
	graph := core.GenreGraph{}
	raw, err := c.knowledgeGet(ctx, "/genres", false)
	if err != nil {
		return graph, err
	}
	z := html.NewTokenizer(strings.NewReader(string(raw)))
	for z.Next() != html.ErrorToken {
		token := z.Token()
		if token.Type != html.StartTagToken || token.Data != "a" {
			continue
		}
		id := ""
		for _, a := range token.Attr {
			if a.Key == "href" && strings.HasPrefix(a.Val, "/genre/") {
				id = strings.TrimPrefix(a.Val, "/genre/")
			}
		}
		if id != "" && z.Next() == html.TextToken {
			graph.Nodes = append(graph.Nodes, core.GenreNode{ID: id, Name: z.Token().Data})
		}
	}
	// Previously retrieved aliases make abbreviated and non-Latin names
	// resolvable without a fresh request or a built-in genre alias table.
	if c.db != nil {
		rows, err := c.db.QueryContext(ctx, "SELECT key,json FROM mb_cache WHERE key LIKE 'knowledge-v1:/genre/%/aliases' ORDER BY key")
		if err == nil {
			for rows.Next() {
				var key, body string
				if rows.Scan(&key, &body) == nil {
					id := strings.TrimSuffix(strings.TrimPrefix(key, "knowledge-v1:/genre/"), "/aliases")
					readGenrePage([]byte(body), id, c.base, &graph, true)
				}
			}
			_ = rows.Close()
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		id := graph.ID(name)
		if id == core.NormalizeIdentityPart(name) || seen[id] {
			continue
		}
		seen[id] = true
		body, e := c.knowledgeGet(ctx, "/genre/"+url.PathEscape(id), false)
		if e != nil {
			continue
		}
		readGenrePage(body, id, c.base, &graph, false)
		if aliases, err := c.knowledgeGet(ctx, "/genre/"+url.PathEscape(id)+"/aliases", false); err == nil {
			readGenrePage(aliases, id, c.base, &graph, true)
		}
	}
	graph.Version = knowledgeHash(graph)
	return graph, nil
}

func knowledgeHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (c *Client) ResolveMusic(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, p ports.Progress) (core.MusicIntent, error) {
	if cat == nil || resolver == nil {
		return intent, nil
	}
	if intent.Knowledge != nil {
		return intent, nil
	} // Saved evidence is immutable.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, KnowledgeBudget)
	defer cancel()
	ctx = context.WithValue(ctx, knowledgeBudgetKey{}, &knowledgeBudget{})
	snapshot := core.KnowledgeSnapshot{}
	if p == nil {
		p = ports.NopProgress{}
	}
	p.Report("generation", 0, 0, "Resolving music references")
	// Album identity must be resolved before the ordinary artist/track resolver.
	for _, group := range []*[]core.IntentReference{&intent.References, &intent.Journey.Waypoints} {
		refs := append([]core.IntentReference(nil), (*group)...)
		for i, ref := range refs {
			if ref.Kind == core.ReferenceAlbum {
				refs[i] = c.resolveAlbum(ctx, ref, cat, resolver, &snapshot)
			}
		}
		*group = refs
	}
	if intent.Destination != nil {
		d := *intent.Destination
		if d.Kind == core.ReferenceAlbum {
			d = c.resolveAlbum(ctx, d, cat, resolver, &snapshot)
		} else {
			r := resolver.ResolveReference(d)
			d.Resolution = &r
			if r.Selected != nil && len(r.Selected.Representatives) > 0 {
				d.TrackID = r.Selected.Representatives[0].TrackID
			}
		}
		intent.Destination = &d
	}
	var genres []string
	for _, g := range intent.Preferences.Genres {
		if g.Influence != core.InfluenceNegative {
			genres = append(genres, g.Value)
		}
	}
	for _, criterion := range intent.EssentialCriteria {
		if criterion.Kind == "genre" || criterion.Kind == "style" {
			genres = append(genres, criterion.Value)
		}
	}
	if len(genres) > 0 {
		p.Report("generation", 0, 0, "Finding genre artists and sampling recordings")
		if err := c.sampleGenreArtists(ctx, &intent, genres, cat, resolver, &snapshot); err != nil {
			return intent, err
		}
		p.Report("generation", 0, 0, "Finding related genres")
		graph, err := c.Graph(ctx, genres)
		snapshot.Graph = graph
		if err != nil {
			snapshot.Notices = append(snapshot.Notices, "Genre relationships unavailable; using local interpretation.")
		}
	}
	// Fetch bounded recording searches; identities must corroborate artist/title.
	queries := append([]string(nil), genres...)
	for _, hint := range intent.GenreExpansions {
		queries = append(queries, hint.RelatedGenres...)
	}
	for _, genre := range genres {
		id := snapshot.Graph.ID(genre)
		related := 0
		for _, edge := range snapshot.Graph.Relations {
			if related >= 3 {
				break
			}
			other := ""
			if edge.From == id {
				other = edge.To
			} else if edge.To == id {
				other = edge.From
			}
			for _, node := range snapshot.Graph.Nodes {
				if node.ID == other {
					queries = append(queries, node.Name)
					related++
					break
				}
			}
		}
	}
	queried := map[string]bool{}
	for _, genre := range queries {
		key := core.NormalizeIdentityPart(genre)
		if queried[key] {
			continue
		}
		queried[key] = true
		query := `tag:"` + mbEscape(genre) + `"`
		for _, period := range intent.Temporal {
			if period.Basis == "original_release" && period.Scope == "playlist" {
				query += fmt.Sprintf(" AND firstreleasedate:[%d TO %d]", period.StartYear, period.EndYear)
			}
		}
		c.searchKnowledgeRecordings(ctx, query, cat, resolver, &snapshot)
	}
	// Enrich starting points with recording-level evidence, never artist tags.
	for _, a := range intent.InferredAnchors {
		r := resolver.ResolveReference(a.Reference)
		if r.Selected == nil || len(r.Selected.Representatives) == 0 {
			continue
		}
		meta, ok := cat.Meta(r.Selected.Representatives[0].TrackID)
		if !ok {
			continue
		}
		c.searchKnowledgeRecordings(ctx, `artist:"`+mbEscape(meta.Ref.Artist)+`" AND recording:"`+mbEscape(meta.Ref.Title)+`"`, cat, resolver, &snapshot)
	}
	for _, period := range intent.Temporal {
		if period.Basis == "composition" {
			for i := range snapshot.Tracks {
				if i >= 4 {
					break
				}
				c.compositionEvidence(ctx, &snapshot.Tracks[i], &snapshot)
			}
			break
		}
	}
	sort.Slice(snapshot.Tracks, func(i, j int) bool { return snapshot.Tracks[i].Ref.ID < snapshot.Tracks[j].Ref.ID })
	snapshot.ID = knowledgeHash(snapshot)
	intent.Knowledge = &snapshot
	if parent.Err() != nil {
		return intent, parent.Err()
	}
	return intent, nil
}

func (c *Client) searchKnowledgeRecordings(ctx context.Context, query string, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) {
	path := "/ws/2/recording?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return
	}
	var body struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	snapshot.Sources = append(snapshot.Sources, c.base+path)
	for _, r := range body.Recordings {
		c.addKnowledgeRecording(r, cat, resolver, snapshot)
	}
}

func (c *Client) addKnowledgeRecording(r mbRecording, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) {
	if len(r.ArtistCredit) == 0 {
		return
	}
	query := r.ArtistCredit[0].Name + " - " + r.Title
	resolved := resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, Query: query})
	if resolved.Status != core.ResolutionResolved || resolved.Selected == nil || len(resolved.Selected.Representatives) == 0 {
		return
	}
	meta, ok := cat.Meta(resolved.Selected.Representatives[0].TrackID)
	if !ok || core.NormalizeIdentityPart(meta.Ref.Title) != core.NormalizeIdentityPart(r.Title) || core.NormalizeIdentityPart(meta.Ref.Artist) != core.NormalizeIdentityPart(r.ArtistCredit[0].Name) {
		return
	}
	for i, prior := range snapshot.Tracks {
		if prior.Ref.ID == meta.Ref.ID {
			if prior.RecordingID != r.ID {
				track := &snapshot.Tracks[i]
				track.IdentityStatus = core.ResolutionAmbiguous
				track.Matched = false
				if track.OriginalReleaseDate != r.FirstReleaseDate {
					track.OriginalReleaseDate = ""
				}
				if len(track.Alternatives) == 0 {
					track.Alternatives = append(track.Alternatives, core.RecordingAlternative{RecordingID: prior.RecordingID, Title: prior.Ref.Title, Artists: prior.AllArtists})
				}
				artists := []string{}
				for _, credit := range r.ArtistCredit {
					artists = append(artists, credit.Name)
				}
				duplicate := false
				for _, alt := range track.Alternatives {
					duplicate = duplicate || alt.RecordingID == r.ID
				}
				if !duplicate {
					track.Alternatives = append(track.Alternatives, core.RecordingAlternative{RecordingID: r.ID, Title: r.Title, Artists: artists, ISRCs: r.ISRCs})
					track.AllArtists = append(track.AllArtists, artists...)
				}
			}
			return
		}
	}
	track := core.EnrichedTrack{Ref: meta.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: r.ID, AllISRCs: r.ISRCs, OriginalReleaseDate: r.FirstReleaseDate}
	for _, credit := range r.ArtistCredit {
		track.AllArtists = append(track.AllArtists, credit.Name)
		track.ArtistIDs = append(track.ArtistIDs, credit.Artist.ID)
	}
	for _, tag := range append(append([]mbTag(nil), r.Genres...), r.Tags...) {
		track.GenreTags = append(track.GenreTags, core.AttributedGenreTag{Name: tag.Name, Votes: tag.Count, Source: "musicbrainz", EntityID: r.ID, Facet: "genre"})
	}
	if len(r.Releases) > 0 {
		track.Album = r.Releases[0].Title
		track.ReleaseID = r.Releases[0].ID
	}
	snapshot.Tracks = append(snapshot.Tracks, track)
	snapshot.Candidates = append(snapshot.Candidates, meta.Ref)
}

func (c *Client) resolveAlbum(ctx context.Context, ref core.IntentReference, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot) core.IntentReference {
	title, artist := ref.Query, ""
	if i := strings.LastIndex(strings.ToLower(title), " by "); i >= 0 {
		title, artist = title[:i], title[i+4:]
	}
	query := `releasegroup:"` + mbEscape(title) + `" AND primarytype:album`
	if artist != "" {
		query += ` AND artist:"` + mbEscape(artist) + `"`
	}
	path := "/ws/2/release-group?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"5"}}.Encode()
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return ref
	}
	var body struct {
		Groups []struct {
			ID           string           `json:"id"`
			Title        string           `json:"title"`
			ArtistCredit []mbArtistCredit `json:"artist-credit"`
		} `json:"release-groups"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ref
	}
	result := core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: resolver.CatalogVersion()}
	for _, g := range body.Groups {
		if core.NormalizeIdentityPart(g.Title) != core.NormalizeIdentityPart(title) || len(g.ArtistCredit) == 0 {
			continue
		}
		if artist != "" && core.NormalizeIdentityPart(g.ArtistCredit[0].Name) != core.NormalizeIdentityPart(artist) {
			continue
		}
		candidate := core.ResolutionCandidate{Kind: core.ReferenceAlbum, EntityID: g.ID, Artist: g.ArtistCredit[0].Name, Title: g.Title, Confidence: 1, Evidence: []core.ResolutionEvidence{{Match: "exact", MatchedText: g.Title}}}
		result.Alternatives = append(result.Alternatives, candidate)
	}
	if len(result.Alternatives) > 1 {
		result.Status = core.ResolutionAmbiguous
	} else if len(result.Alternatives) == 1 {
		candidate := result.Alternatives[0]
		albumSnapshot := core.KnowledgeSnapshot{}
		c.searchKnowledgeRecordings(ctx, "rgid:"+candidate.EntityID, cat, resolver, &albumSnapshot)
		snapshot.Tracks = append(snapshot.Tracks, albumSnapshot.Tracks...)
		snapshot.Candidates = append(snapshot.Candidates, albumSnapshot.Candidates...)
		snapshot.Sources = append(snapshot.Sources, albumSnapshot.Sources...)
		for _, t := range albumSnapshot.Tracks {
			candidate.Representatives = append(candidate.Representatives, core.WeightedTrack{TrackID: t.Ref.ID})
		}
		for i := range candidate.Representatives {
			candidate.Representatives[i].Weight = 1 / float64(len(candidate.Representatives))
		}
		if len(candidate.Representatives) > 0 {
			result.Status = core.ResolutionResolved
			result.Selected = &candidate
			ref.TrackID = candidate.Representatives[0].TrackID
		}
	}
	ref.Resolution = &result
	return ref
}

func (c *Client) compositionEvidence(ctx context.Context, track *core.EnrichedTrack, snapshot *core.KnowledgeSnapshot) {
	if track.RecordingID == "" || track.IdentityStatus != core.ResolutionResolved {
		return
	}
	path := "/ws/2/recording/" + url.PathEscape(track.RecordingID) + "?fmt=json&inc=work-rels"
	raw, err := c.knowledgeGet(ctx, path, false)
	if err != nil {
		return
	}
	var recording struct {
		Relations []struct {
			Type string `json:"type"`
			Work struct {
				ID string `json:"id"`
			} `json:"work"`
		} `json:"relations"`
	}
	if json.Unmarshal(raw, &recording) != nil {
		return
	}
	for _, rel := range recording.Relations {
		if rel.Type != "performance" || rel.Work.ID == "" {
			continue
		}
		path = "/ws/2/work/" + url.PathEscape(rel.Work.ID) + "?fmt=json&inc=artist-rels"
		raw, err = c.knowledgeGet(ctx, path, false)
		if err != nil {
			return
		}
		var work struct {
			Relations []struct {
				Type  string `json:"type"`
				Begin string `json:"begin"`
				End   string `json:"end"`
			} `json:"relations"`
		}
		if json.Unmarshal(raw, &work) != nil {
			return
		}
		for _, credit := range work.Relations {
			if credit.Type == "composer" && yearOf(credit.Begin) > 0 {
				track.CompositionStartYear = yearOf(credit.Begin)
				track.CompositionEndYear = yearOf(credit.End)
				if track.CompositionEndYear == 0 {
					track.CompositionEndYear = track.CompositionStartYear
				}
				track.WorkID = rel.Work.ID
				snapshot.Sources = append(snapshot.Sources, c.base+path)
				return
			}
		}
	}
}

func negativeKnowledge(raw string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil {
		return false
	}
	for _, key := range []string{"recordings", "release-groups", "artists"} {
		if value, ok := object[key]; ok && strings.TrimSpace(string(value)) == "[]" {
			return true
		}
	}
	return false
}
