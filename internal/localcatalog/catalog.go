package localcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
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
	"github.com/platten/playlistai/internal/librarysearch"
	"github.com/platten/playlistai/internal/searchwork"
	"github.com/platten/playlistai/internal/sqliteuri"
)

var namespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var indexUpgradeMu sync.Mutex

// Options is immutable after Open. RootMappings maps logical aliases from the
// pack to absolute roots on this machine. Missing roots are accepted as
// offline; relative roots and aliases absent from the manifest are rejected.
type Options struct {
	// ProfileGeneration is the verified companion SHA-256 for shared packs.
	ProfileGeneration string
	ProfilePath       string
	Shared            bool
	SourceID          string
	RootMappings      map[string]string
	PageSize          int
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
	profiles   *sql.DB
	dspStats   *librarylearn.DSPStatisticsModel
	indexes    *derivedIndexes
	closeIndex func()
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
			CLAPGeneration: manifest.CLAPGeneration,
		},
		prefix: "local:" + options.SourceID + ":", pageSize: pageSize, rootMap: rootMap,
		budget: acquireQueryBudget(generation),
	}
	if options.Shared {
		catalog.prefix = "pack:" + options.SourceID + ":"
		catalog.provenance.Source = "shared_pack"
		catalog.provenance.ProfileGeneration = options.ProfileGeneration
		catalog.rootMap = nil
	}
	// Old derivatives remain recoverable while a new version is built atomically.
	// Existing but corrupt current indexes still fail verification, never rebuild.
	indexUpgradeMu.Lock()
	_, statErr := os.Stat(filepath.Join(generation.Directory(), derivedIndexDir, "manifest.json"))
	var upgradeErr error
	if errors.Is(statErr, os.ErrNotExist) {
		upgradeErr = BuildIndexes(context.Background(), generation, IndexBuildOptions{Workers: 2, ShardRows: 16_384, MaxScratchBytes: 256 << 20})
	}
	indexUpgradeMu.Unlock()
	if upgradeErr != nil {
		releaseQueryBudget(generation, catalog.budget)
		lease.Release()
		return nil, fmt.Errorf("localcatalog: upgrade search indexes: %w", upgradeErr)
	}
	indexes, closeIndex, indexErr := openDerivedIndexes(context.Background(), generation)
	if indexErr != nil {
		releaseQueryBudget(generation, catalog.budget)
		lease.Release()
		return nil, fmt.Errorf("localcatalog: open required search indexes: %w", indexErr)
	}
	catalog.indexes, catalog.closeIndex = indexes, closeIndex
	if options.Shared && options.ProfilePath != "" {
		dsn, err := sqliteuri.ReadOnly(options.ProfilePath, true)
		if err != nil {
			_ = catalog.Close()
			return nil, err
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			_ = catalog.Close()
			return nil, err
		}
		catalog.profiles = db
		var packID string
		if err := db.QueryRow("SELECT pack_id FROM packs WHERE pack_id=? AND sha256=?", manifest.PackID, generation.PackSHA256()).Scan(&packID); err != nil {
			_ = catalog.Close()
			return nil, fmt.Errorf("localcatalog: companion pack binding: %w", err)
		}
	}
	if learned, learningErr := generation.MetadataBasis(context.Background()); learningErr == nil && learned.Version != "" {
		catalog.metadata = &learned
	}
	if raw, ok, statisticsErr := generation.Statistics(context.Background()); statisticsErr == nil && ok {
		var statistics librarylearn.DSPStatisticsModel
		if json.Unmarshal(raw, &statistics) == nil {
			catalog.dspStats = &statistics
		}
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
	closeIndex := c.closeIndex
	c.lease, c.generation = nil, nil
	c.budget = nil
	c.indexes, c.closeIndex = nil, nil
	c.mu.Unlock()
	if closeIndex != nil {
		closeIndex()
	}
	if c.profiles != nil {
		_ = c.profiles.Close()
	}
	releaseQueryBudget(generation, budget)
	lease.Release()
	return nil
}

func (c *Catalog) Manifest() librarypack.Manifest           { return c.manifest }
func (c *Catalog) Provenance() Provenance                   { return c.provenance }
func (c *Catalog) VectorSpace() librarypack.VectorSpace     { return c.manifest.MERT }
func (c *Catalog) CLAPVectorSpace() librarypack.VectorSpace { return c.manifest.CLAP }

func (c *Catalog) NamespacedID(localID string) string { return c.prefix + localID }

func (c *Catalog) owns(id string) bool {
	return strings.HasPrefix(id, c.prefix) && len(id) > len(c.prefix)
}

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

// AudioDuplicates returns exact compatible Chromaprint duplicates from the
// pinned generation. Results are namespace-qualified and canonical by ID.
func (c *Catalog) AudioDuplicates(ctx context.Context, id string, limit int) ([]Track, error) {
	localID, err := c.localID(id)
	if err != nil {
		return nil, err
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	tracks, err := generation.AudioDuplicates(ctx, localID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]Track, len(tracks))
	for index := range tracks {
		result[index] = c.convertTrack(tracks[index])
	}
	return result, nil
}

// Duplicates returns high-confidence recording duplicates based on valid ISRC,
// recording MBID, AcoustID ID, or locally corroborated Chromaprint evidence.
func (c *Catalog) Duplicates(ctx context.Context, id string, limit int) ([]Track, error) {
	localID, err := c.localID(id)
	if err != nil {
		return nil, err
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	tracks, err := generation.Duplicates(ctx, localID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]Track, len(tracks))
	for index := range tracks {
		result[index] = c.convertTrack(tracks[index])
	}
	return result, nil
}

// ArtistRecordings uses the generation's exact normalized artist index. It is
// intentionally separate from fuzzy free-text resolution so artist-only
// requests do not depend on dense catalog row iteration.
func (c *Catalog) ArtistRecordings(ctx context.Context, artist string) ([]core.TrackRef, error) {
	return c.artistRecordings(ctx, artist, -1)
}

func (c *Catalog) artistRecordings(ctx context.Context, artist string, limit int) ([]core.TrackRef, error) {
	key := normalizeUnicode(artist)
	if key == "" {
		return []core.TrackRef{}, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := c.indexes.metadata.QueryContext(ctx, "SELECT id FROM artists WHERE artist=? ORDER BY id LIMIT ?", key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.TrackRef, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		track, ok, err := generation.Lookup(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("localcatalog: artist index refers to a missing track")
		}
		result = append(result, core.TrackRef{ID: c.NamespacedID(track.ID), Artist: track.Artist, Title: track.Title, RecordingIdentity: track.RecordingIdentity})
	}
	return result, rows.Err()
}

func (c *Catalog) convertTrack(track librarypack.Track) Track {
	capabilities := append([]string(nil), track.Capabilities...)
	missingness := append([]byte(nil), track.Missingness...)
	var fingerprint *librarypack.AudioFingerprint
	if track.AudioFingerprint != nil {
		copy := *track.AudioFingerprint
		fingerprint = &copy
	}
	return Track{
		ID: c.NamespacedID(track.ID), LocalID: track.ID, Artist: track.Artist, Title: track.Title,
		NormalizedArtist: track.NormalizedArtist, NormalizedTitle: track.NormalizedTitle,
		SourceIdentity: track.SourceIdentity, RecordingIdentity: track.RecordingIdentity,
		ISRC: track.ISRC, MusicBrainzRecording: track.MusicBrainzRecording, AcoustID: track.AcoustID,
		AudioFingerprint:     fingerprint,
		DurationMilliseconds: track.DurationMilliseconds, DurationProvenance: track.DurationProvenance, DurationReliable: track.DurationReliable,
		Cluster: track.Cluster, ClusterScore: track.ClusterScore, AlternativeCluster: track.Alternative, AlternativeScore: track.AltScore,
		AlbumArtist: track.AlbumArtist, Album: track.Album, Capabilities: capabilities,
		Missingness: missingness, Failure: track.Failure, Unsupported: track.Unsupported,
		Provenance: c.provenance,
	}
}

// Search performs bounded-memory Unicode-aware metadata search over the
// generation's deterministic inverted index and retains only the best limit.
func (c *Catalog) Search(ctx context.Context, query MetadataQuery) ([]Hit, error) {
	if query.Limit <= 0 {
		return []Hit{}, nil
	}
	if query.Limit > 10_000 {
		query.Limit = 10_000
	}
	if query.Artist != "" {
		refs, err := c.artistRecordings(ctx, query.Artist, -1)
		if err != nil {
			return nil, err
		}
		hits := make([]Hit, 0, min(len(refs), query.Limit))
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, excluded := query.ExcludeIDs[ref.ID]; excluded {
				continue
			}
			track, found, err := c.Lookup(ctx, ref.ID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, errors.New("localcatalog: artist index refers to a missing track")
			}
			hits = append(hits, Hit{Track: track, Evidence: Evidence{Channel: MetadataChannel, Rank: len(hits) + 1, Score: 1, QueryID: query.Artist, Provenance: c.provenance}})
			if len(hits) >= query.Limit {
				break
			}
		}
		return hits, nil
	}
	normalized := normalizeUnicode(query.Text)
	terms := strings.Fields(normalized)
	if len(terms) == 0 && query.Criterion == nil && len(query.AllCriteria) == 0 {
		return []Hit{}, nil
	}
	// Keep the generated join bounded and comfortably below SQLite's host
	// parameter/table limits even for adversarially long search text.
	if len(terms) > 32 {
		return nil, errors.New("localcatalog: metadata query contains too many terms")
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()

	releaseCPU, err := searchwork.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseCPU()

	type scored struct {
		track librarypack.Track
		score float64
	}
	best := make([]scored, 0, query.Limit)
	querySQL := "SELECT t0.id FROM terms t0"
	arguments := make([]any, 0, len(terms)*2)
	for index := 1; index < len(terms); index++ {
		querySQL += fmt.Sprintf(" JOIN terms t%d ON t%d.id=t0.id", index, index)
	}
	querySQL += " WHERE "
	for index, term := range terms {
		if index > 0 {
			querySQL += " AND "
		}
		querySQL += fmt.Sprintf("t%d.term>=? AND t%d.term<?", index, index)
		arguments = append(arguments, term, term+"\U0010ffff")
	}
	querySQL += " ORDER BY t0.id"
	if len(query.AllCriteria) > 0 {
		if len(query.AllCriteria) > 8 {
			return nil, errors.New("localcatalog: too many conjunctive criteria")
		}
		querySQL = "SELECT t0.id FROM terms t0"
		arguments = arguments[:0]
		for index, criterion := range query.AllCriteria {
			if !supportsDirectAnnotationCriterion(criterion) {
				return nil, errors.New("localcatalog: conjunctive criterion has no posting")
			}
			if index > 0 {
				querySQL += fmt.Sprintf(" JOIN terms t%d ON t%d.id=t0.id", index, index)
			}
			arguments = append(arguments, criterionPostingTerm(criterion))
		}
		querySQL += " WHERE "
		for index := range query.AllCriteria {
			if index > 0 {
				querySQL += " AND "
			}
			querySQL += fmt.Sprintf("t%d.term=?", index)
		}
		querySQL += " ORDER BY t0.id"
	} else if query.Criterion != nil && supportsDirectAnnotationCriterion(*query.Criterion) {
		querySQL = "SELECT id FROM terms WHERE term=? ORDER BY id"
		arguments = []any{criterionPostingTerm(*query.Criterion)}
	}
	rows, err := c.indexes.metadata.QueryContext(ctx, querySQL, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var localID string
		if err := rows.Scan(&localID); err != nil {
			return nil, err
		}
		track, ok, err := generation.Lookup(ctx, localID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("localcatalog: metadata index refers to a missing track")
		}
		id := c.NamespacedID(track.ID)
		if _, excluded := query.ExcludeIDs[id]; excluded {
			continue
		}
		if len(query.AllCriteria) > 0 {
			valid := true
			for _, criterion := range query.AllCriteria {
				if annotationCriterionEvidence(annotations(track.RawTags), criterion) != core.EvidenceMatch {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			best = append(best, scored{track: track, score: 1})
			if len(best) >= query.Limit {
				break
			}
			continue
		}
		if query.Criterion != nil {
			if annotationCriterionEvidence(annotations(track.RawTags), *query.Criterion) != core.EvidenceMatch {
				continue
			}
			// A typed posting already establishes the requested annotation.
			// It is a bounded retrieval page, not a learned free-text ranking:
			// scanning every matching recording to refine its metadata score can
			// consume the entire prompt budget for common genres.
			best = append(best, scored{track: track, score: 1})
			if len(best) >= query.Limit {
				break
			}
			continue
		}
		textScore, textOK := metadataScore(normalized, terms, track)
		learnedScore, learnedOK := c.learnedMetadataScore(ctx, generation, normalized, track)
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
	if err := rows.Err(); err != nil {
		return nil, err
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

func (c *Catalog) learnedMetadataScore(ctx context.Context, generation *librarypack.Generation, query string, track librarypack.Track) (float64, bool) {
	if c.metadata == nil || query == "" {
		return 0, false
	}
	queryTerms := strings.Fields(query)
	// Vocabulary entries are complete genre phrases. Preserve an exact phrase
	// before considering individual terms and keep sparse columns canonical.
	queryTerms = append(queryTerms, strings.TrimSpace(query))
	sort.Strings(queryTerms)
	queryRow := librarylearn.SparseRow{}
	seenColumns := map[int]bool{}
	for _, term := range queryTerms {
		column := sort.SearchStrings(c.metadata.Vocabulary, term)
		if column < len(c.metadata.Vocabulary) && c.metadata.Vocabulary[column] == term {
			if seenColumns[column] {
				continue
			}
			seenColumns[column] = true
			weight := 1.0
			if column < len(c.metadata.IDF) && c.metadata.IDF[column] > 0 {
				weight = c.metadata.IDF[column]
			}
			queryRow.Values = append(queryRow.Values, librarylearn.SparseValue{Column: column, Value: weight})
		}
	}
	if len(queryRow.Values) == 0 {
		return 0, false
	}
	var queryNorm float64
	for _, value := range queryRow.Values {
		queryNorm += value.Value * value.Value
	}
	queryNorm = math.Sqrt(queryNorm)
	for index := range queryRow.Values {
		queryRow.Values[index].Value /= queryNorm
	}
	artist := strings.TrimSpace(strings.Split(track.Artist, "; ")[0])
	sum := sha256.Sum256([]byte("artist\x00" + strings.ToLower(strings.Join(strings.Fields(artist), " "))))
	row, ok, err := generation.MetadataRow(ctx, "artist:"+hex.EncodeToString(sum[:12]))
	if err != nil || !ok {
		return 0, false
	}
	var baseline float64
	for left, right := 0, 0; left < len(row.Values) && right < len(queryRow.Values); {
		switch {
		case row.Values[left].Column < queryRow.Values[right].Column:
			left++
		case row.Values[left].Column > queryRow.Values[right].Column:
			right++
		default:
			baseline += row.Values[left].Value * queryRow.Values[right].Value
			left++
			right++
		}
	}
	latent := 0.0
	if c.metadata.SVD.Outcome == librarylearn.SVDAvailable {
		left, right := c.metadata.SVD.Project(row), c.metadata.SVD.Project(queryRow)
		var dotProduct, leftNorm, rightNorm float64
		for index := range min(len(left), len(right)) {
			dotProduct += left[index] * right[index]
			leftNorm += left[index] * left[index]
			rightNorm += right[index] * right[index]
		}
		if leftNorm > 0 && rightNorm > 0 {
			latent = dotProduct / math.Sqrt(leftNorm*rightNorm)
		}
	}
	if baseline <= 0 && latent <= 0 {
		return 0, false
	}
	return baseline + .5*max(0, latent), true
}

// CriterionEvidence exposes sourced recording annotations. Absence is
// unknown, never proof of a mismatch.
func (c *Catalog) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if !supportsAnnotationCriterion(criterion) {
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
	return annotationCriterionEvidence(annotations(track.RawTags), criterion)
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
	var values []string
	for _, annotation := range annotations(raw) {
		switch annotation.Kind {
		case "genre", "style", "mood", "instrumentation", "language", "work", "movement", "composer", "album_composer", "title", "artist_credit", "album_artist":
			values = append(values, annotation.Value)
		}
	}
	return strings.Join(values, " ")
}

// Neighbors is the exact, bounded-memory cosine oracle over the pinned
// generation's immutable packed shards. It never compares vectors from another
// representation or walks metadata rows to discover vector IDs.
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
	if c.indexes == nil || c.indexes.mert == nil {
		return nil, ErrNoVector
	}
	exclude := make(map[string]struct{}, len(query.ExcludeIDs)+1)
	if seedLocalID != "" {
		exclude[seedLocalID] = struct{}{}
	}
	for id := range query.ExcludeIDs {
		if localID, localErr := c.localID(id); localErr == nil {
			exclude[localID] = struct{}{}
		}
	}
	best, err := c.indexes.mert.Search(ctx, librarysearch.Query{Vector: vector, Limit: query.Limit, Exclude: exclude, Workers: 1})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, len(best))
	space := c.manifest.MERT
	for i, item := range best {
		track, ok, lookupErr := generation.Lookup(ctx, item.ID)
		if lookupErr != nil || !ok {
			if lookupErr == nil {
				lookupErr = errors.New("localcatalog: MERT index refers to a missing track")
			}
			return nil, lookupErr
		}
		hits[i] = Hit{Track: c.convertTrack(track), Evidence: Evidence{
			Channel: MERTChannel, Rank: i + 1, Score: item.Score,
			QueryID: query.SeedID, Provenance: c.provenance, VectorSpace: &space,
		}}
	}
	return hits, nil
}

// CLAPNeighbors performs exact cosine retrieval in the pack's aligned CLAP
// audio/text space. It is deliberately separate from MERT retrieval.
func (c *Catalog) CLAPNeighbors(ctx context.Context, query NeighborQuery) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query.Limit <= 0 {
		return []Hit{}, nil
	}
	if query.Limit > 10_000 {
		query.Limit = 10_000
	}
	if query.Space != nil && *query.Space != c.manifest.CLAP {
		return nil, ErrIncompatibleSpace
	}
	if (query.SeedID == "") == (len(query.Vector) == 0) {
		return nil, errors.New("localcatalog: provide exactly one CLAP seed or vector")
	}
	if len(query.Vector) > 0 && query.Space == nil {
		return nil, ErrIncompatibleSpace
	}
	model := c.manifest.CLAPModel
	if len(query.Vector) > 0 {
		if model != nil && (query.CLAPModel == nil || *query.CLAPModel != *model) {
			return nil, ErrIncompatibleSpace
		}
		if query.CLAPModel != nil {
			manifest := c.manifest
			manifest.CLAPModel = query.CLAPModel
			if !manifest.CLAPCompatible(*query.CLAPModel) {
				return nil, ErrIncompatibleSpace
			}
		}
		model = query.CLAPModel
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
		vector, ok, err = generation.CLAPVector(ctx, seedLocalID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNoVector
		}
		if c.manifest.CLAPModel == nil {
			evidence, _, evidenceErr := generation.CLAPEvidence(ctx, seedLocalID)
			if evidenceErr != nil {
				return nil, evidenceErr
			}
			if evidence != nil {
				model = evidence.Model
			}
		}
	}
	if !validQueryVector(vector, c.manifest.CLAP.Dimension) {
		return nil, errors.New("localcatalog: invalid normalized CLAP query vector")
	}
	if c.indexes == nil || c.indexes.clap == nil {
		return nil, ErrNoVector
	}
	exclude := make(map[string]struct{}, len(query.ExcludeIDs)+1)
	if seedLocalID != "" {
		exclude[seedLocalID] = struct{}{}
	}
	for id := range query.ExcludeIDs {
		if localID, localErr := c.localID(id); localErr == nil {
			exclude[localID] = struct{}{}
		}
	}
	if c.manifest.Version >= librarypack.FormatVersion && c.manifest.CLAPModel == nil {
		// Until derivatives index per-row identities, mixed packs need one
		// streamed compatibility pass. Bound both JSON work and exclusion memory;
		// larger mixed packs retain metadata/MERT and per-record assessment.
		const maxMixedCLAPCompatibilityTracks = 50_000
		if c.manifest.Coverage.Tracks > maxMixedCLAPCompatibilityTracks {
			return nil, ErrIncompatibleSpace
		}
		source, sourceErr := generation.OpenTrackSource(ctx)
		if sourceErr != nil {
			return nil, sourceErr
		}
		defer source.Close()
		wanted := c.clapEvidenceSource(model).SpaceID
		for {
			track, ok, sourceErr := source.Next(ctx)
			if sourceErr != nil {
				return nil, sourceErr
			}
			if !ok {
				break
			}
			if len(track.CLAP) == 0 {
				continue
			}
			var rowModel *core.AudioModelIdentity
			if track.CLAPEvidence != nil {
				rowModel = track.CLAPEvidence.Model
			}
			if c.clapEvidenceSource(rowModel).SpaceID != wanted {
				exclude[track.ID] = struct{}{}
			}
		}
	}
	best, err := c.indexes.clap.Search(ctx, librarysearch.Query{Vector: vector, Limit: query.Limit, Exclude: exclude, Workers: 1})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, len(best))
	space := c.manifest.CLAP
	for i, item := range best {
		track, ok, lookupErr := generation.Lookup(ctx, item.ID)
		if lookupErr != nil || !ok {
			if lookupErr == nil {
				lookupErr = errors.New("localcatalog: CLAP index refers to a missing track")
			}
			return nil, lookupErr
		}
		hits[i] = Hit{Track: c.convertTrack(track), Evidence: Evidence{Channel: CLAPChannel, Rank: i + 1, Score: item.Score, QueryID: query.SeedID, Provenance: c.provenance, VectorSpace: &space}}
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
