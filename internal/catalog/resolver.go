package catalog

import (
	"math"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const (
	maxAlternatives       = 5
	maxRepresentatives    = 4
	representativePoolMax = 128
)

type indexedTrack struct {
	row int
	ref core.TrackRef
}

func (c *Catalog) CatalogVersion() string { return c.version }

// ResolveReference resolves artists and tracks in separate namespaces. Exact
// normalized names and aliases win before prefix/token fallback. No query word
// is silently removed.
func (c *Catalog) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	key := c.version + "\x00" + string(ref.Kind) + "\x00" + ref.TrackID + "\x00" + normalizeUnicodeSearch(ref.Query)
	c.resolutionMu.RLock()
	if cached, ok := c.resolutionCache[key]; ok {
		c.resolutionMu.RUnlock()
		return cloneResolution(cached)
	}
	c.resolutionMu.RUnlock()

	var result core.ReferenceResolution
	if ref.TrackID != "" {
		result = c.resolveID(ref.Kind, ref.TrackID)
	} else if ref.Kind == core.ReferenceArtist {
		result = c.resolveArtist(ref.Query)
	} else {
		result = c.resolveTrack(ref.Query)
	}
	if result.Status == core.ResolutionUnresolved {
		tokens := tokenize(ref.Query)
		lean := dropFiller(tokens)
		if len(lean) > 0 && len(lean) < len(tokens) {
			query := strings.Join(lean, " ")
			if ref.Kind == core.ReferenceArtist {
				result = c.resolveArtist(query)
			} else {
				result = c.resolveTrack(query)
			}
			markFillerFallback(&result, ref.Query)
		}
	}
	result.CatalogVersion = c.version
	c.resolutionMu.Lock()
	c.resolutionCache[key] = cloneResolution(result)
	c.resolutionMu.Unlock()
	return result
}

func markFillerFallback(result *core.ReferenceResolution, original string) {
	adjust := func(candidate *core.ResolutionCandidate) {
		candidate.Confidence *= .9
		candidate.Evidence = append(candidate.Evidence, core.ResolutionEvidence{
			Match: "filler", NormalizedQuery: normalizeUnicodeSearch(original), MatchedText: "removed known natural-language filler words",
		})
	}
	if result.Selected != nil {
		adjust(result.Selected)
	}
	for i := range result.Alternatives {
		adjust(&result.Alternatives[i])
	}
}

func (c *Catalog) resolveID(kind core.ReferenceKind, id string) core.ReferenceResolution {
	meta, ok := c.Meta(id)
	if !ok {
		return unresolved()
	}
	if kind == core.ReferenceArtist {
		candidate := c.artistCandidate(meta.Ref.Artist, 1, "id", id)
		return resolved(candidate)
	}
	candidate := trackCandidate(meta.Ref, 1, "id", id)
	return resolved(candidate)
}

func (c *Catalog) resolveArtist(query string) core.ReferenceResolution {
	latin, unicodeQuery := normalizeSearch(query), normalizeUnicodeSearch(query)
	if latin == "" && unicodeQuery == "" {
		return unresolved()
	}

	aliases := c.aliasArtists(latin, unicodeQuery)
	rows := c.artistSearchRows(latin, unicodeQuery)
	byArtist := make(map[string]string)
	matchKind := make(map[string]string)
	for _, artist := range aliases {
		key := normalizeUnicodeSearch(artist)
		byArtist[key], matchKind[key] = artist, "alias"
	}
	for _, row := range rows {
		artistKey := normalizeUnicodeSearch(row.ref.Artist)
		latinArtist := normalizeSearch(row.ref.Artist)
		if !containsTokens(latinArtist, strings.Fields(latin)) &&
			!containsTokens(artistKey, strings.Fields(unicodeQuery)) {
			continue
		}
		switch {
		case (latin != "" && latinArtist == latin) || artistKey == unicodeQuery:
			byArtist[artistKey], matchKind[artistKey] = row.ref.Artist, "exact"
		case matchKind[artistKey] == "":
			byArtist[artistKey], matchKind[artistKey] = row.ref.Artist, artistFallbackKind(latin, unicodeQuery, latinArtist, artistKey)
		}
	}

	var candidates []core.ResolutionCandidate
	for key, artist := range byArtist {
		kind := matchKind[key]
		confidence := map[string]float64{"exact": 1, "alias": .98, "prefix": .88, "tokens": .76}[kind]
		candidates = append(candidates, c.artistCandidate(artist, confidence, kind, query))
	}
	return rankResolution(candidates)
}

func artistFallbackKind(latinQuery, unicodeQuery, latinArtist, unicodeArtist string) string {
	if (latinQuery != "" && strings.HasPrefix(latinArtist, latinQuery+" ")) || strings.HasPrefix(unicodeArtist, unicodeQuery+" ") {
		return "prefix"
	}
	return "tokens"
}

func (c *Catalog) resolveTrack(query string) core.ReferenceResolution {
	latin, unicodeQuery := normalizeSearch(query), normalizeUnicodeSearch(query)
	if latin == "" && unicodeQuery == "" {
		return unresolved()
	}
	rows := c.trackSearchRows(latin, unicodeQuery)
	seenRecording := make(map[string]struct{})
	candidates := make([]core.ResolutionCandidate, 0, maxAlternatives*2)
	for _, row := range rows {
		displayLatin := normalizeSearch(row.ref.Display())
		titleLatin := normalizeSearch(row.ref.Title)
		displayUnicode := normalizeUnicodeSearch(row.ref.Display())
		titleUnicode := normalizeUnicodeSearch(row.ref.Title)
		kind, confidence := "tokens", .74
		switch {
		case (latin != "" && displayLatin == latin) || displayUnicode == unicodeQuery:
			kind, confidence = "exact", 1
		case (latin != "" && titleLatin == latin) || titleUnicode == unicodeQuery:
			kind, confidence = "exact", .97
		case (latin != "" && (strings.HasPrefix(displayLatin, latin+" ") || strings.HasPrefix(titleLatin, latin+" "))) ||
			strings.HasPrefix(displayUnicode, unicodeQuery+" ") || strings.HasPrefix(titleUnicode, unicodeQuery+" "):
			kind, confidence = "prefix", .86
		}
		recording := normalizeUnicodeSearch(row.ref.Artist) + "\x00" + normalizeUnicodeSearch(row.ref.Title)
		if _, duplicate := seenRecording[recording]; duplicate {
			continue
		}
		seenRecording[recording] = struct{}{}
		candidates = append(candidates, trackCandidate(row.ref, confidence, kind, query))
	}
	return rankResolution(candidates)
}

func rankResolution(candidates []core.ResolutionCandidate) core.ReferenceResolution {
	if len(candidates) == 0 {
		return unresolved()
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence > candidates[j].Confidence
		}
		if candidates[i].Artist != candidates[j].Artist {
			return normalizeUnicodeSearch(candidates[i].Artist) < normalizeUnicodeSearch(candidates[j].Artist)
		}
		return candidates[i].EntityID < candidates[j].EntityID
	})
	if len(candidates) > maxAlternatives {
		candidates = candidates[:maxAlternatives]
	}
	// Equal or near-equal high-quality entities require an explicit choice.
	if len(candidates) > 1 && candidates[0].Confidence-candidates[1].Confidence <= .02 {
		return core.ReferenceResolution{Status: core.ResolutionAmbiguous, Alternatives: candidates}
	}
	selected := candidates[0]
	return core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &selected, Alternatives: candidates[1:]}
}

func resolved(candidate core.ResolutionCandidate) core.ReferenceResolution {
	return core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &candidate}
}

func unresolved() core.ReferenceResolution {
	return core.ReferenceResolution{Status: core.ResolutionUnresolved, Alternatives: []core.ResolutionCandidate{}}
}

func trackCandidate(ref core.TrackRef, confidence float64, match, query string) core.ResolutionCandidate {
	return core.ResolutionCandidate{
		Kind: core.ReferenceTrack, EntityID: ref.ID, Artist: ref.Artist, Title: ref.Title,
		Confidence:      confidence,
		Evidence:        []core.ResolutionEvidence{{Match: match, NormalizedQuery: normalizeUnicodeSearch(query), MatchedText: ref.Display()}},
		Representatives: []core.WeightedTrack{{TrackID: ref.ID, Weight: 1}},
	}
}

func (c *Catalog) artistCandidate(artist string, confidence float64, match, query string) core.ResolutionCandidate {
	representatives := c.artistRepresentatives(artist)
	return core.ResolutionCandidate{
		Kind: core.ReferenceArtist, EntityID: "artist:" + normalizeUnicodeSearch(artist), Artist: artist,
		Confidence:      confidence,
		Evidence:        []core.ResolutionEvidence{{Match: match, NormalizedQuery: normalizeUnicodeSearch(query), MatchedText: artist}},
		Representatives: representatives,
	}
}

func (c *Catalog) artistSearchRows(latin, unicodeQuery string) []indexedTrack {
	if latin != "" {
		return c.queryTracks("search", strings.Fields(latin))
	}
	if c.hasUnicodeSearch {
		return c.queryTracks("unicode_search", strings.Fields(unicodeQuery))
	}
	return c.scanTracks(func(ref core.TrackRef) bool {
		return containsTokens(normalizeUnicodeSearch(ref.Artist), strings.Fields(unicodeQuery))
	})
}

func (c *Catalog) trackSearchRows(latin, unicodeQuery string) []indexedTrack {
	if latin != "" {
		return c.queryTracks("search", strings.Fields(latin))
	}
	if c.hasUnicodeSearch {
		return c.queryTracks("unicode_search", strings.Fields(unicodeQuery))
	}
	return c.scanTracks(func(ref core.TrackRef) bool {
		return containsTokens(normalizeUnicodeSearch(ref.Display()), strings.Fields(unicodeQuery))
	})
}

func (c *Catalog) queryTracks(column string, tokens []string) []indexedTrack {
	if len(tokens) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString("SELECT row, id, artist, title FROM tracks WHERE ")
	args := make([]any, 0, len(tokens))
	for i, token := range tokens {
		if i > 0 {
			query.WriteString(" AND ")
		}
		query.WriteString(column + " LIKE ?")
		args = append(args, "%"+token+"%")
	}
	query.WriteString(" ORDER BY row")
	rows, err := c.db.Query(query.String(), args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []indexedTrack
	for rows.Next() {
		var track indexedTrack
		if rows.Scan(&track.row, &track.ref.ID, &track.ref.Artist, &track.ref.Title) == nil {
			out = append(out, track)
		}
	}
	return out
}

func (c *Catalog) scanTracks(keep func(core.TrackRef) bool) []indexedTrack {
	rows, err := c.db.Query("SELECT row, id, artist, title FROM tracks ORDER BY row")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []indexedTrack
	for rows.Next() {
		var track indexedTrack
		if rows.Scan(&track.row, &track.ref.ID, &track.ref.Artist, &track.ref.Title) == nil && keep(track.ref) {
			out = append(out, track)
		}
	}
	return out
}

func containsTokens(text string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(text, token) {
			return false
		}
	}
	return len(tokens) > 0
}

func (c *Catalog) aliasArtists(latin, unicodeQuery string) []string {
	if !c.hasAliases {
		return nil
	}
	rows, err := c.db.Query(
		"SELECT DISTINCT artist FROM artist_aliases WHERE alias_search = ? OR alias_unicode = ? ORDER BY artist",
		latin, unicodeQuery,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var artists []string
	for rows.Next() {
		var artist string
		if rows.Scan(&artist) == nil {
			artists = append(artists, artist)
		}
	}
	return artists
}

func (c *Catalog) artistRepresentatives(artist string) []core.WeightedTrack {
	artistKey := normalizeUnicodeSearch(artist)
	key := c.version + "\x00" + artistKey
	c.resolutionMu.RLock()
	if cached, ok := c.representativeCache[key]; ok {
		c.resolutionMu.RUnlock()
		return append([]core.WeightedTrack(nil), cached...)
	}
	c.resolutionMu.RUnlock()

	latin := normalizeSearch(artist)
	var rows []indexedTrack
	if latin != "" {
		rows = c.queryTracks("search", strings.Fields(latin))
	} else if c.hasUnicodeSearch {
		rows = c.queryTracks("unicode_search", strings.Fields(artistKey))
	} else {
		rows = c.scanTracks(func(ref core.TrackRef) bool { return normalizeUnicodeSearch(ref.Artist) == artistKey })
	}
	filtered := rows[:0]
	for _, row := range rows {
		if normalizeUnicodeSearch(row.ref.Artist) == artistKey {
			filtered = append(filtered, row)
		}
	}
	rows = filtered
	if len(rows) > representativePoolMax {
		bounded := make([]indexedTrack, representativePoolMax)
		for i := range bounded {
			bounded[i] = rows[i*(len(rows)-1)/(representativePoolMax-1)]
		}
		rows = bounded
	}
	var representatives []core.WeightedTrack
	if c.vec == nil { // metadata-only resolver fixtures and degraded catalogs
		limit := min(maxRepresentatives, len(rows))
		for _, row := range rows[:limit] {
			representatives = append(representatives, core.WeightedTrack{TrackID: row.ref.ID, Weight: 1 / float64(limit)})
		}
	} else {
		representatives = c.medoidRepresentatives(rows, maxRepresentatives)
	}
	c.resolutionMu.Lock()
	c.representativeCache[key] = append([]core.WeightedTrack(nil), representatives...)
	c.resolutionMu.Unlock()
	return representatives
}

func (c *Catalog) medoidRepresentatives(tracks []indexedTrack, k int) []core.WeightedTrack {
	if len(tracks) == 0 {
		return nil
	}
	if k > len(tracks) {
		k = len(tracks)
	}
	vectors := make([]ports.Vectors, len(tracks))
	valid := tracks[:0]
	validVectors := vectors[:0]
	for _, track := range tracks {
		if value, ok := c.VectorsByRow(track.row); ok {
			valid = append(valid, track)
			validVectors = append(validVectors, value)
		}
	}
	tracks, vectors = valid, validVectors
	if len(tracks) == 0 {
		return nil
	}
	if k > len(tracks) {
		k = len(tracks)
	}
	distance := func(i, j int) float64 {
		return 1 - .5*(cosine(vectors[i].Audio, vectors[j].Audio)+cosine(vectors[i].Track, vectors[j].Track))
	}
	medoids := []int{bestMedoid(len(tracks), distance)}
	for len(medoids) < k {
		best, bestDistance := -1, -1.0
		for i := range tracks {
			if containsInt(medoids, i) {
				continue
			}
			nearest := math.MaxFloat64
			for _, medoid := range medoids {
				nearest = math.Min(nearest, distance(i, medoid))
			}
			if nearest > bestDistance || (nearest == bestDistance && (best < 0 || tracks[i].ref.ID < tracks[best].ref.ID)) {
				best, bestDistance = i, nearest
			}
		}
		medoids = append(medoids, best)
	}
	assignments := assignClusters(len(tracks), medoids, distance)
	for cluster := range medoids {
		members := clusterMembers(assignments, cluster)
		medoids[cluster] = bestAmong(members, distance, tracks)
	}
	assignments = assignClusters(len(tracks), medoids, distance)
	out := make([]core.WeightedTrack, len(medoids))
	for cluster, medoid := range medoids {
		out[cluster] = core.WeightedTrack{TrackID: tracks[medoid].ref.ID, Weight: float64(len(clusterMembers(assignments, cluster))) / float64(len(tracks))}
	}
	return out
}

func bestMedoid(n int, distance func(int, int) float64) int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return bestAmong(indices, distance, nil)
}

func bestAmong(indices []int, distance func(int, int) float64, tracks []indexedTrack) int {
	best, bestCost := indices[0], math.MaxFloat64
	for _, candidate := range indices {
		var cost float64
		for _, other := range indices {
			cost += distance(candidate, other)
		}
		if cost < bestCost || (cost == bestCost && tracks != nil && tracks[candidate].ref.ID < tracks[best].ref.ID) {
			best, bestCost = candidate, cost
		}
	}
	return best
}

func assignClusters(n int, medoids []int, distance func(int, int) float64) []int {
	out := make([]int, n)
	for i := 0; i < n; i++ {
		if own := indexOfInt(medoids, i); own >= 0 {
			out[i] = own
			continue
		}
		best, bestDistance := 0, math.MaxFloat64
		for cluster, medoid := range medoids {
			if d := distance(i, medoid); d < bestDistance {
				best, bestDistance = cluster, d
			}
		}
		out[i] = best
	}
	return out
}

func containsInt(values []int, value int) bool { return indexOfInt(values, value) >= 0 }

func indexOfInt(values []int, value int) int {
	for i, existing := range values {
		if existing == value {
			return i
		}
	}
	return -1
}

func clusterMembers(assignments []int, cluster int) []int {
	var out []int
	for i, assigned := range assignments {
		if assigned == cluster {
			out = append(out, i)
		}
	}
	return out
}

func cosine(a, b []float32) float64 {
	var dot, aa, bb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func cloneResolution(in core.ReferenceResolution) core.ReferenceResolution {
	out := in
	out.Alternatives = append([]core.ResolutionCandidate(nil), in.Alternatives...)
	for i := range out.Alternatives {
		out.Alternatives[i].Evidence = append([]core.ResolutionEvidence(nil), out.Alternatives[i].Evidence...)
		out.Alternatives[i].Representatives = append([]core.WeightedTrack(nil), out.Alternatives[i].Representatives...)
	}
	if in.Selected != nil {
		selected := *in.Selected
		selected.Evidence = append([]core.ResolutionEvidence(nil), selected.Evidence...)
		selected.Representatives = append([]core.WeightedTrack(nil), selected.Representatives...)
		out.Selected = &selected
	}
	return out
}

var _ ports.ReferenceResolver = (*Catalog)(nil)
