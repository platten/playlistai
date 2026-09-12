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
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/httpretry"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

const KnowledgeBudget = 30 * time.Second
const KnowledgeRequests = 20

type knowledgeBudgetKey struct{}
type cacheOnlyKey struct{}
type knowledgeBudget struct {
	requests int
	backoffs map[string]time.Time
}

// knowledgeGet caches exact provider responses, not prompts. Stale cache is
// usable offline. Only valid responses enter the cache; outages aren't misses.
func (c *Client) knowledgeGet(ctx context.Context, path string, negative bool) ([]byte, error) {
	return c.metadataGet(ctx, c.base, path, "knowledge-v1:", c.hc, negative)
}

func (c *Client) metadataGet(ctx context.Context, base, path, namespace string, client *http.Client, _ bool) ([]byte, error) {
	key := metadataKey(base, path, namespace)
	ttl := musicBrainzTTL
	if namespace == "deezer-seeds-v1:" {
		ttl = 24 * time.Hour
	}
	if namespace == "discogs-v1:" {
		ttl = discogsTTL
	}
	var cached cachedResponse
	var epoch uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cached = c.readCache(ctx, key)
		if cached.body != "" && !validMetadata(path, namespace, []byte(cached.body)) {
			cached = cachedResponse{}
		}
		age := time.Since(time.Unix(cached.fetched, 0))
		if cached.body != "" && age >= 0 && age < ttl {
			return []byte(cached.body), nil
		}
		// Discogs content must not be served after its separate freshness limit.
		if namespace == "discogs-v1:" {
			cached = cachedResponse{}
		}
		if only, _ := ctx.Value(cacheOnlyKey{}).(bool); only {
			if cached.body != "" {
				return []byte(cached.body), nil
			}
			return nil, core.ErrUnavailable
		}
		owner, done, generation := c.takeFetch(key)
		if owner {
			epoch = generation
			defer c.finishFetch(key)
			// A writer may have completed between the read and gate acquisition.
			if latest := c.readCache(ctx, key); latest.body != "" && time.Since(time.Unix(latest.fetched, 0)) >= 0 && time.Since(time.Unix(latest.fetched, 0)) < ttl && validMetadata(path, namespace, []byte(latest.body)) {
				return []byte(latest.body), nil
			}
			break
		}
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	fallback := func(err error) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if cached.body != "" {
			return []byte(cached.body), nil
		}
		return nil, err
	}
	if budget != nil {
		if budget.requests >= KnowledgeRequests {
			return fallback(fmt.Errorf("metadata request budget exhausted"))
		}
		if delay := time.Until(budget.backoffs[namespace]); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return fallback(ctx.Err())
			case <-timer.C:
			}
		}
		ctx = httpretry.WithAttemptCheck(ctx, func() error {
			if budget.requests >= KnowledgeRequests {
				return fmt.Errorf("metadata request budget exhausted")
			}
			budget.requests++
			return nil
		})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return fallback(err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json,text/html")
	req.Header.Set("Accept-Language", "en")
	resp, err := client.Do(req)
	if err != nil {
		return fallback(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if budget != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
			wait := 5 * time.Second
			if value := resp.Header.Get("Retry-After"); value != "" {
				wait = httpretry.RetryAfter(value, time.Now())
			}
			if budget.backoffs == nil {
				budget.backoffs = make(map[string]time.Time)
			}
			budget.backoffs[namespace] = time.Now().Add(wait)
		}
		return fallback(fmt.Errorf("music metadata HTTP %d", resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return fallback(fmt.Errorf("invalid metadata response"))
	}
	if !validMetadata(path, namespace, raw) {
		return fallback(fmt.Errorf("invalid metadata response"))
	}
	// Cache only the public fields used by the Discogs fallback, not images,
	// marketplace data or user information returned alongside release metadata.
	if namespace == "discogs-v1:" {
		var err error
		raw, err = sanitizeDiscogs(path, raw)
		if err != nil {
			return fallback(err)
		}
	}
	c.writeCache(ctx, key, raw, epoch)
	return raw, nil
}

func validMetadata(path, namespace string, raw []byte) bool {
	if namespace == "knowledge-v1:" && (path == "/genres" || strings.HasPrefix(path, "/genre/")) {
		body := strings.ToLower(string(raw))
		if path == "/genres" {
			return strings.Contains(body, "href=\"/genre/") || strings.Contains(body, "href='/genre/")
		}
		return strings.Contains(body, "<html") || strings.Contains(body, "<a ") || strings.Contains(body, "<table")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	for _, key := range []string{"error", "message"} {
		if value := object[key]; len(value) > 0 && string(value) != "null" {
			return false
		}
	}
	if namespace == "discogs-v1:" && strings.HasPrefix(path, "/releases/") {
		_, err := sanitizeDiscogs(path, raw)
		return err == nil
	}
	// Endpoint envelopes must be present. A successful empty array is valid;
	// an unrelated JSON object or provider error is not a negative result.
	endpoint, _, _ := strings.Cut(path, "?")
	field := map[string]string{"/ws/2/recording": "recordings", "/ws/2/artist": "artists", "/ws/2/release-group": "release-groups", "/database/search": "results"}[endpoint]
	if field != "" {
		var values []json.RawMessage
		value, ok := object[field]
		return ok && strings.HasPrefix(strings.TrimSpace(string(value)), "[") && json.Unmarshal(value, &values) == nil
	}
	return true
}

func (c *Client) IsCachedGenre(ctx context.Context, name string) bool {
	if data := c.localDataset(); data != nil && data.HasGenre(ctx, name) {
		return true
	}
	graph, err := c.GenreNames(context.WithValue(ctx, cacheOnlyKey{}, true))
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

// GenreNames loads identity only. Slow relationship pages must not consume the
// discovery budget before a counted genre has even been classified.
func (c *Client) GenreNames(ctx context.Context) (core.GenreGraph, error) {
	if data := c.localDataset(); data != nil {
		if graph, err := data.Genres(ctx); err == nil && len(graph.Nodes) > 0 {
			return graph, nil
		}
	}
	return c.onlineGenreNames(ctx)
}

func (c *Client) onlineGenreNames(ctx context.Context) (core.GenreGraph, error) {
	graph := core.GenreGraph{}
	raw, err := c.knowledgeGet(ctx, "/genres", false)
	if err != nil {
		return graph, err
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return graph, err
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			id, ok := strings.CutPrefix(nodeAttr(node, "href"), "/genre/")
			// Names are wrapped in <bdi> on the live site, not direct text.
			if name := nodeText(node); ok && id != "" && !strings.ContainsAny(id, "/?#") && name != "" {
				graph.Nodes = append(graph.Nodes, core.GenreNode{ID: id, Name: name})
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if len(graph.Nodes) == 0 {
		return graph, fmt.Errorf("genre index contains no readable genre names")
	}
	// Previously retrieved aliases make abbreviated and non-Latin names
	// resolvable without a fresh request or a built-in genre alias table.
	aliases := c.cachedAliases(ctx)
	ids := make([]string, 0, len(aliases))
	for id := range aliases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		readGenrePage([]byte(aliases[id]), id, c.base, &graph, true)
	}
	graph.Version = knowledgeHash(graph)
	return graph, nil
}

// Graph enriches the name index with the requested genre relationships.
func (c *Client) Graph(ctx context.Context, names []string) (core.GenreGraph, error) {
	graph, err := c.GenreNames(ctx)
	if err != nil {
		return graph, err
	}
	seen := map[string]bool{}
	for _, name := range names {
		id := graph.ID(name)
		if id == core.NormalizeIdentityPart(name) || seen[id] || strings.HasPrefix(id, "discogs-dump:") {
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
	graph.Version = ""
	graph.Version = knowledgeHash(graph)
	return graph, nil
}

func knowledgeHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type iterativeKnowledgeKey struct{}

func (c *Client) PrepareMusic(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, p ports.Progress) (core.MusicIntent, error) {
	return c.ResolveMusic(context.WithValue(ctx, iterativeKnowledgeKey{}, true), intent, cat, resolver, p)
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
	// A cold cache must recognize the same categories as a warm one. Check
	// provider genre identity before treating a bare category as an artist.
	if name := rules.BareGenreQuery(intent.OriginalDescription); name != "" && len(intent.EssentialCriteria) == 0 && len(intent.Preferences.Genres) == 0 && len(intent.Preferences.Styles) == 0 {
		graph, err := c.GenreNames(ctx)
		if err == nil && c.localDataset() != nil && graph.ID(name) == core.NormalizeIdentityPart(name) {
			graph, err = c.onlineGenreNames(ctx)
		}
		if err == nil {
			for _, node := range graph.Nodes {
				if node.ID == graph.ID(name) {
					intent = rules.ApplyConfirmedGenre(intent, name)
					break
				}
			}
		} else {
			snapshot.Notices = append(snapshot.Notices, "Genre-name lookup unavailable; the bare description could not be checked against provider categories.")
		}
	}
	// Explicit missing artists take priority over broad genre discovery.
	intent = c.resolveMissingArtists(ctx, intent, cat, resolver, &snapshot, p)
	if core.WantsInstrumental(intent) && len(intent.Seeds.TrackIDs) == 0 && len(intent.Required.TrackIDs) == 0 {
		c.discoverInstrumental(ctx, &intent, cat, resolver, &snapshot, p)
	}
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
		} else if d.Resolution == nil || d.Resolution.Status != core.ResolutionResolved || d.Resolution.CatalogVersion != resolver.CatalogVersion() {
			r := resolver.ResolveReference(d)
			d.Resolution = &r
			if r.Selected != nil && len(r.Selected.Representatives) > 0 {
				d.TrackID = r.Selected.Representatives[0].TrackID
			}
		}
		intent.Destination = &d
	}
	genres := discoveryGenres(intent)
	iterative, _ := ctx.Value(iterativeKnowledgeKey{}).(bool)
	if iterative {
		seen := map[string]bool{}
		for _, genre := range genres {
			key := core.NormalizeIdentityPart(genre)
			if seen[key] {
				continue
			}
			seen[key] = true
			if data := c.localDataset(); data != nil && data.Compatible(resolver.CatalogVersion()) && data.HasGenre(ctx, genre) {
				continue
			}
			p.Report("generation", 0, 0, "Finding artists for the requested genre")
			pool := c.genreArtists(ctx, genre)
			snapshot.ArtistPools = append(snapshot.ArtistPools, pool)
			snapshot.Sources = append(snapshot.Sources, pool.Sources...)
		}
	}
	if len(genres) > 0 && !iterative {
		// Ordinary genre requests need the same priority as journey stages.
		// Otherwise artist sampling can exhaust the budget before any recording
		// with track-level genre evidence is retrieved.
		p.Report("generation", 0, 0, "Finding recordings for requested genres")
		seen := map[string]bool{}
		for _, genre := range genres {
			key := core.NormalizeIdentityPart(genre)
			if !seen[key] {
				seen[key] = true
				query := `tag:"` + mbEscape(genre) + `"`
				if intent.Mode == core.ModeJourney {
					// Reserve a first page for every stage before expanding any.
					c.searchKnowledgeRecordings(ctx, query, cat, resolver, &snapshot)
				} else {
					c.searchKnowledgeRecordings(ctx, query, cat, resolver, &snapshot, intent.Controls.TotalTrackCount)
				}
			}
		}
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
		if iterative && len(genres) > 0 {
			break
		}
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
		if ctx.Err() != nil {
			break
		}
		r := resolver.ResolveReference(a.Reference)
		if r.Selected == nil || len(r.Selected.Representatives) == 0 {
			continue
		}
		meta, ok := cat.Meta(r.Selected.Representatives[0].TrackID)
		if !ok {
			continue
		}
		known := false
		for _, track := range snapshot.Tracks {
			known = known || track.Ref.ID == meta.Ref.ID
		}
		if known {
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
	c.acousticTracks(ctx, snapshot.Tracks, 25)
	snapshot.ID = knowledgeHash(snapshot)
	intent.Knowledge = &snapshot
	if parent.Err() != nil {
		return intent, parent.Err()
	}
	return intent, nil
}

func (c *Client) searchKnowledgeRecordings(ctx context.Context, query string, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, targets ...int) {
	pages := 1
	if len(targets) > 0 {
		pages = 3
	}
	for page := 0; page < pages && ctx.Err() == nil; page++ {
		values := url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}
		if page > 0 {
			values.Set("offset", fmt.Sprint(page*100))
		}
		path := "/ws/2/recording?" + values.Encode()
		raw, err := c.knowledgeGet(ctx, path, false)
		if err != nil {
			return
		}
		var body struct {
			Count      int           `json:"count"`
			Recordings []mbRecording `json:"recordings"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return
		}
		snapshot.Sources = append(snapshot.Sources, c.base+path)
		for _, r := range body.Recordings {
			if ctx.Err() != nil {
				break
			}
			c.addKnowledgeRecording(r, cat, resolver, snapshot)
		}
		// Expand identity lookup, never relax musical eligibility. All pages
		// share the existing provider request and time budgets.
		if len(body.Recordings) < 100 || body.Count <= (page+1)*100 || len(targets) > 0 && len(snapshot.Candidates) >= targets[0] {
			break
		}
	}
}

func (c *Client) addKnowledgeRecording(r mbRecording, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, knownIDs ...string) {
	if len(r.ArtistCredit) == 0 {
		return
	}
	query := r.ArtistCredit[0].Name + " - " + r.Title
	reference := core.IntentReference{Kind: core.ReferenceTrack, Query: query}
	if len(knownIDs) > 0 {
		reference.TrackID = knownIDs[0]
	}
	resolved := resolver.ResolveReference(reference)
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
				track.Acoustic = nil
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
	// Reserve part of the shared deadline for recovery from a provider outage.
	mbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	title, artist := ref.Query, ""
	if a, t, ok := core.QualifiedReferenceParts(ref.Query); ok {
		title, artist = t, a
	}
	query := `releasegroup:"` + mbEscape(title) + `" AND primarytype:album`
	if artist != "" {
		query += ` AND artist:"` + mbEscape(artist) + `"`
	}
	path := "/ws/2/release-group?" + url.Values{"query": {query}, "fmt": {"json"}, "limit": {"100"}}.Encode()
	raw, err := c.knowledgeGet(mbCtx, path, false)
	if err != nil {
		if c.MetadataStatus().DiscogsConfigured && ctx.Err() == nil {
			if found, ok := c.resolveDiscogsAlbum(ctx, ref, cat, resolver, snapshot); ok {
				return found
			}
		}
		snapshot.Notices = append(snapshot.Notices, fmt.Sprintf("MusicBrainz album lookup for %q was unavailable: %v. Trying Deezer album metadata.", ref.Query, err))
		return c.resolveDeezerAlbum(ctx, ref, cat, resolver, snapshot)
	}
	snapshot.Sources = append(snapshot.Sources, c.base+path)
	var body struct {
		Count  int `json:"count"`
		Groups []struct {
			ID           string           `json:"id"`
			Title        string           `json:"title"`
			ArtistCredit []mbArtistCredit `json:"artist-credit"`
		} `json:"release-groups"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return c.resolveDeezerAlbum(ctx, ref, cat, resolver, snapshot)
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
	if len(result.Alternatives) > 1 || body.Count > len(body.Groups) {
		result.Status = core.ResolutionAmbiguous
		snapshot.Notices = append(snapshot.Notices, "The album search is ambiguous or incomplete. Add the artist and exact album title to narrow the lookup.")
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
	if result.Status == core.ResolutionUnresolved {
		return c.resolveDeezerAlbum(ctx, ref, cat, resolver, snapshot)
	}
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
