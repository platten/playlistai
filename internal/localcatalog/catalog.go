package localcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
)

var namespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Options is immutable after Open. RootMappings maps logical aliases from the
// pack to absolute roots on this machine. Missing roots are accepted as
// offline; relative roots and aliases absent from the manifest are rejected.
type Options struct {
	SourceID     string
	RootMappings map[string]string
	PageSize     int
}

// Catalog owns one librarypack lease. Close releases it after in-flight calls
// finish. Holding the lease is what makes replacement/removal safe for readers.
type Catalog struct {
	lease      *librarypack.Lease
	generation *librarypack.Generation
	manifest   librarypack.Manifest
	provenance Provenance
	prefix     string
	pageSize   int
	rootMap    map[string]string
	metadata   *librarylearn.MetadataModel
	budget     *sharedQueryBudget
	executorMu sync.Mutex
	executor   *Executor

	mu     sync.RWMutex
	closed bool
}

// Open consumes lease on success. On validation failure it releases the lease.
func Open(lease *librarypack.Lease, options Options) (*Catalog, error) {
	if lease == nil || lease.Generation() == nil {
		return nil, errors.New("localcatalog: a live generation lease is required")
	}
	if !namespacePattern.MatchString(options.SourceID) {
		lease.Release()
		return nil, errors.New("localcatalog: invalid source namespace")
	}
	generation := lease.Generation()
	manifest := generation.Manifest()
	aliases := make(map[string]struct{}, len(manifest.RootAliases))
	for _, alias := range manifest.RootAliases {
		aliases[alias] = struct{}{}
	}
	rootMap := make(map[string]string, len(options.RootMappings))
	for alias, root := range options.RootMappings {
		if _, ok := aliases[alias]; !ok {
			lease.Release()
			return nil, fmt.Errorf("localcatalog: root alias %q is absent from the pack", alias)
		}
		if !filepath.IsAbs(root) {
			lease.Release()
			return nil, fmt.Errorf("localcatalog: root mapping %q must be absolute", alias)
		}
		rootMap[alias] = filepath.Clean(root)
	}
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = 512
	}
	if pageSize > 10_000 {
		pageSize = 10_000
	}
	catalog := &Catalog{
		lease: lease, generation: generation, manifest: manifest,
		provenance: Provenance{
			Source: "local_library", SourceID: options.SourceID,
			PackID: manifest.PackID, PackSHA256: generation.PackSHA256(),
			CorpusGeneration: manifest.CorpusGeneration, MetadataGeneration: manifest.MetadataGeneration,
			MERTGeneration: manifest.MERTGeneration,
		},
		prefix: "local:" + options.SourceID + ":", pageSize: pageSize, rootMap: rootMap,
		budget: acquireQueryBudget(generation),
	}
	if raw, learningErr := generation.Learning(context.Background()); learningErr == nil && string(raw) != "{}" {
		var learned struct {
			Metadata librarylearn.MetadataModel `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &learned); err != nil {
			releaseQueryBudget(generation, catalog.budget)
			lease.Release()
			return nil, fmt.Errorf("localcatalog: decode learned metadata: %w", err)
		}
		catalog.metadata = &learned.Metadata
	}
	return catalog, nil
}

func (c *Catalog) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	lease := c.lease
	generation, budget := c.generation, c.budget
	c.lease, c.generation = nil, nil
	c.budget = nil
	c.mu.Unlock()
	releaseQueryBudget(generation, budget)
	lease.Release()
	return nil
}

func (c *Catalog) Manifest() librarypack.Manifest       { return c.manifest }
func (c *Catalog) Provenance() Provenance               { return c.provenance }
func (c *Catalog) VectorSpace() librarypack.VectorSpace { return c.manifest.MERT }

func (c *Catalog) NamespacedID(localID string) string { return c.prefix + localID }

func (c *Catalog) localID(id string) (string, error) {
	if !strings.HasPrefix(id, c.prefix) || len(id) == len(c.prefix) {
		return "", ErrInvalidID
	}
	return strings.TrimPrefix(id, c.prefix), nil
}

func (c *Catalog) withGeneration() (*librarypack.Generation, func(), error) {
	c.mu.RLock()
	if c.closed || c.generation == nil {
		c.mu.RUnlock()
		return nil, nil, ErrClosed
	}
	return c.generation, c.mu.RUnlock, nil
}

func (c *Catalog) Lookup(ctx context.Context, id string) (Track, bool, error) {
	localID, err := c.localID(id)
	if err != nil {
		return Track{}, false, err
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return Track{}, false, err
	}
	defer done()
	track, ok, err := generation.Lookup(ctx, localID)
	if err != nil || !ok {
		return Track{}, ok, err
	}
	return c.convertTrack(track), true, nil
}

func (c *Catalog) convertTrack(track librarypack.Track) Track {
	capabilities := append([]string(nil), track.Capabilities...)
	missingness := append([]byte(nil), track.Missingness...)
	return Track{
		ID: c.NamespacedID(track.ID), LocalID: track.ID, Artist: track.Artist, Title: track.Title,
		AlbumArtist: track.AlbumArtist, Album: track.Album, Capabilities: capabilities,
		Missingness: missingness, Failure: track.Failure, Unsupported: track.Unsupported,
		Provenance: c.provenance,
	}
}

// Search performs bounded-memory Unicode-aware metadata search. It scans the
// immutable generation in canonical ID pages and retains only the best limit.
func (c *Catalog) Search(ctx context.Context, query MetadataQuery) ([]Hit, error) {
	if query.Limit <= 0 {
		return []Hit{}, nil
	}
	if query.Limit > 10_000 {
		query.Limit = 10_000
	}
	normalized := normalizeUnicode(query.Text)
	terms := strings.Fields(normalized)
	if len(terms) == 0 {
		return []Hit{}, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()

	type scored struct {
		track librarypack.Track
		score float64
	}
	best := make([]scored, 0, query.Limit)
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := generation.List(ctx, after, c.pageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, track := range page {
			id := c.NamespacedID(track.ID)
			if _, excluded := query.ExcludeIDs[id]; excluded {
				continue
			}
			textScore, textOK := metadataScore(normalized, terms, track)
			learnedScore, learnedOK := c.learnedMetadataScore(normalized, track)
			if textOK || learnedOK {
				score := textScore
				if learnedOK {
					score += 2 * learnedScore
				}
				best = append(best, scored{track: track, score: score})
				sort.Slice(best, func(i, j int) bool {
					if best[i].score != best[j].score {
						return best[i].score > best[j].score
					}
					return best[i].track.ID < best[j].track.ID
				})
				if len(best) > query.Limit {
					best = best[:query.Limit]
				}
			}
		}
		after = page[len(page)-1].ID
		if len(page) < c.pageSize {
			break
		}
	}
	hits := make([]Hit, len(best))
	for i, item := range best {
		hits[i] = Hit{Track: c.convertTrack(item.track), Evidence: Evidence{
			Channel: MetadataChannel, Rank: i + 1, Score: item.score,
			QueryID: query.Text, Provenance: c.provenance,
		}}
	}
	return hits, nil
}

func (c *Catalog) learnedMetadataScore(query string, track librarypack.Track) (float64, bool) {
	if c.metadata == nil || query == "" {
		return 0, false
	}
	column := sort.SearchStrings(c.metadata.Vocabulary, query)
	if column >= len(c.metadata.Vocabulary) || c.metadata.Vocabulary[column] != query {
		return 0, false
	}
	artist := strings.TrimSpace(strings.Split(track.Artist, "; ")[0])
	sum := sha256.Sum256([]byte("artist\x00" + strings.ToLower(strings.Join(strings.Fields(artist), " "))))
	row, ok := c.metadata.Row("artist:" + hex.EncodeToString(sum[:12]))
	if !ok {
		return 0, false
	}
	index := sort.Search(len(row.Values), func(i int) bool { return row.Values[i].Column >= column })
	if index == len(row.Values) || row.Values[index].Column != column || row.Values[index].Value <= 0 {
		return 0, false
	}
	return row.Values[index].Value, true
}

// CriterionEvidence exposes only sourced local genre annotations. Absence is
// unknown, never proof of a mismatch.
func (c *Catalog) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if criterion.Kind != "genre" {
		return core.EvidenceUnknown
	}
	localID, err := c.localID(id)
	if err != nil {
		return core.EvidenceUnknown
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return core.EvidenceUnknown
	}
	defer done()
	track, ok, err := generation.Lookup(ctx, localID)
	if err != nil || !ok {
		return core.EvidenceUnknown
	}
	var tags map[string]string
	if json.Unmarshal(track.RawTags, &tags) != nil {
		return core.EvidenceUnknown
	}
	want := normalizeUnicode(criterion.Value)
	for key, value := range tags {
		if strings.EqualFold(strings.TrimSpace(key), "genre") && normalizeUnicode(value) == want {
			return core.EvidenceMatch
		}
	}
	return core.EvidenceUnknown
}

func metadataScore(query string, terms []string, track librarypack.Track) (float64, bool) {
	title := normalizeUnicode(track.Title)
	artist := normalizeUnicode(track.Artist)
	albumArtist := normalizeUnicode(track.AlbumArtist)
	album := normalizeUnicode(track.Album)
	tags := normalizeUnicode(rawTagText(track.RawTags))
	display := strings.TrimSpace(artist + " " + title + " " + albumArtist + " " + album + " " + tags)
	for _, term := range terms {
		if !strings.Contains(display, term) {
			return 0, false
		}
	}
	score := float64(len(terms)) / float64(max(1, len(strings.Fields(display))))
	switch {
	case title == query:
		score += 4
	case artist+" "+title == query:
		score += 3
	case strings.Contains(title, query):
		score += 2
	case strings.Contains(artist, query):
		score += 1
	}
	return score, true
}

func rawTagText(raw json.RawMessage) string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var values []string
	var visit func(any)
	visit = func(item any) {
		switch typed := item.(type) {
		case string:
			values = append(values, typed)
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(typed[key])
			}
		}
	}
	visit(value)
	return strings.Join(values, " ")
}

// Neighbors is the exact, bounded-memory cosine oracle for the pack's MERT
// space. It never compares or returns vectors from another representation.
func (c *Catalog) Neighbors(ctx context.Context, query NeighborQuery) ([]Hit, error) {
	if query.Limit <= 0 {
		return []Hit{}, nil
	}
	if query.Limit > 10_000 {
		query.Limit = 10_000
	}
	if query.Space != nil && *query.Space != c.manifest.MERT {
		return nil, ErrIncompatibleSpace
	}
	if (query.SeedID == "") == (len(query.Vector) == 0) {
		return nil, errors.New("localcatalog: provide exactly one MERT seed or vector")
	}
	if len(query.Vector) > 0 && query.Space == nil {
		return nil, ErrIncompatibleSpace
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	vector := append([]float32(nil), query.Vector...)
	seedLocalID := ""
	if query.SeedID != "" {
		seedLocalID, err = c.localID(query.SeedID)
		if err != nil {
			return nil, err
		}
		var ok bool
		vector, ok, err = generation.Vector(ctx, seedLocalID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNoVector
		}
	}
	if !validQueryVector(vector, c.manifest.MERT.Dimension) {
		return nil, errors.New("localcatalog: invalid normalized query vector")
	}

	type scored struct {
		track librarypack.Track
		score float64
	}
	best := make([]scored, 0, query.Limit)
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := generation.List(ctx, after, c.pageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, track := range page {
			id := c.NamespacedID(track.ID)
			if track.ID == seedLocalID {
				continue
			}
			if _, excluded := query.ExcludeIDs[id]; excluded {
				continue
			}
			if !hasCapability(track.Capabilities, "mert") {
				continue
			}
			candidate, ok, err := generation.Vector(ctx, track.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			score := dot(vector, candidate)
			best = append(best, scored{track: track, score: score})
			sort.Slice(best, func(i, j int) bool {
				if best[i].score != best[j].score {
					return best[i].score > best[j].score
				}
				return best[i].track.ID < best[j].track.ID
			})
			if len(best) > query.Limit {
				best = best[:query.Limit]
			}
		}
		after = page[len(page)-1].ID
		if len(page) < c.pageSize {
			break
		}
	}
	hits := make([]Hit, len(best))
	space := c.manifest.MERT
	for i, item := range best {
		hits[i] = Hit{Track: c.convertTrack(item.track), Evidence: Evidence{
			Channel: MERTChannel, Rank: i + 1, Score: item.score,
			QueryID: query.SeedID, Provenance: c.provenance, VectorSpace: &space,
		}}
	}
	return hits, nil
}

func validQueryVector(vector []float32, dimension int) bool {
	if dimension <= 0 || len(vector) != dimension {
		return false
	}
	var normSquared float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		normSquared += float64(value) * float64(value)
	}
	normValue := math.Sqrt(normSquared)
	return normValue >= 0.98 && normValue <= 1.02
}

func dot(left, right []float32) float64 {
	var sum float64
	for i := range left {
		sum += float64(left[i]) * float64(right[i])
	}
	return sum
}

func hasCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func normalizeUnicode(value string) string {
	decomposed := norm.NFKD.String(value)
	var builder strings.Builder
	pendingSpace, wrote := false, false
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = unicode.ToLower(r)
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if pendingSpace && wrote {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			pendingSpace, wrote = false, true
		} else {
			pendingSpace = true
		}
	}
	return builder.String()
}
