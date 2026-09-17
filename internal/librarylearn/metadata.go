// Package librarylearn implements deterministic, unsupervised learning over a
// frozen local-library generation. Physical worker counts never participate in
// IDs, ordering, sampling, or numerical merge order.
package librarylearn

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const MetadataVersion = "library-metadata-tfidf/v1"

type MetadataTrack struct {
	TrackID        string
	AlbumID        string
	ArtistIDs      []string
	AlbumArtistIDs []string
	Genres         []string
}

type MetadataSource interface {
	Next(context.Context) (MetadataTrack, bool, error)
}

type SliceMetadataSource struct {
	Rows  []MetadataTrack
	index int
}

func (s *SliceMetadataSource) Next(ctx context.Context) (MetadataTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return MetadataTrack{}, false, err
	}
	if s.index >= len(s.Rows) {
		return MetadataTrack{}, false, nil
	}
	row := s.Rows[s.index]
	s.index++
	return row, true, nil
}

type ArtistAlbumAssociation struct {
	ArtistID string
	AlbumID  string
	Role     string
}

type SparseValue struct {
	Column int
	Value  float64
}

type SparseRow struct {
	ArtistID string
	Values   []SparseValue
}

type MetadataOptions struct {
	Workers         int
	SVDDim          int
	SVDIterations   int
	MaxScratchBytes int64
}

type SVDOutcome string

const (
	SVDAvailable             SVDOutcome = "available"
	SVDInsufficientStructure SVDOutcome = "insufficient_structure"
	SVDResourceLimited       SVDOutcome = "resource_limited"
)

type MetadataModel struct {
	Version      string
	Vocabulary   []string
	IDF          []float64
	Rows         []SparseRow
	Associations []ArtistAlbumAssociation
	SVD          TruncatedSVD
}

func (m MetadataModel) Row(artistID string) (SparseRow, bool) {
	index := sort.Search(len(m.Rows), func(i int) bool { return m.Rows[i].ArtistID >= artistID })
	if index == len(m.Rows) || m.Rows[index].ArtistID != artistID {
		return SparseRow{}, false
	}
	return m.Rows[index], true
}

// WeightedGenreCosine compares the normalized TF-IDF baseline. Missing rows or
// rows without genre evidence return ok=false rather than an invented score.
func (m MetadataModel) WeightedGenreCosine(leftArtist, rightArtist string) (score float64, ok bool) {
	left, leftOK := m.Row(leftArtist)
	right, rightOK := m.Row(rightArtist)
	if !leftOK || !rightOK || len(left.Values) == 0 || len(right.Values) == 0 {
		return 0, false
	}
	for a, b := 0, 0; a < len(left.Values) && b < len(right.Values); {
		switch {
		case left.Values[a].Column < right.Values[b].Column:
			a++
		case right.Values[b].Column < left.Values[a].Column:
			b++
		default:
			score += left.Values[a].Value * right.Values[b].Value
			a++
			b++
		}
	}
	return max(-1, min(1, score)), true
}

// BuildMetadata constructs artist rows from distinct artist-album
// associations. Within an album, its distinct genres share one unit of weight,
// preventing track counts and repeated tags from dominating the model. Track
// artists drive the learned rows; album-artist relationships remain separately
// represented. When track credits are unavailable, album artists are used as a
// transparent fallback.
func BuildMetadata(ctx context.Context, tracks []MetadataTrack, options MetadataOptions) (MetadataModel, error) {
	if err := ctx.Err(); err != nil {
		return MetadataModel{}, err
	}
	canonical := append([]MetadataTrack(nil), tracks...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].TrackID < canonical[j].TrackID })
	return BuildMetadataSource(ctx, &SliceMetadataSource{Rows: canonical}, options)
}

// BuildMetadataSource consumes a frozen canonical stream without retaining one
// object per track. Source IDs must be strictly increasing; only distinct
// artist/album/genre relationships remain in memory.
func BuildMetadataSource(ctx context.Context, source MetadataSource, options MetadataOptions) (MetadataModel, error) {
	if source == nil {
		return MetadataModel{}, errors.New("librarylearn: metadata source is required")
	}
	workers := max(1, options.Workers)
	type albumGenres map[string]map[string]struct{}
	byArtist := map[string]albumGenres{}
	assocSet := map[ArtistAlbumAssociation]struct{}{}
	genreSet := map[string]struct{}{}

	lastTrackID := ""
	for {
		track, ok, err := source.Next(ctx)
		if err != nil {
			return MetadataModel{}, err
		}
		if !ok {
			break
		}
		track.TrackID = strings.TrimSpace(track.TrackID)
		if track.TrackID == "" || lastTrackID != "" && track.TrackID <= lastTrackID {
			return MetadataModel{}, errors.New("librarylearn: metadata source IDs must be unique and strictly increasing")
		}
		lastTrackID = track.TrackID
		genres := canonicalStrings(track.Genres)
		artists := canonicalIDs(track.ArtistIDs)
		albumArtists := canonicalIDs(track.AlbumArtistIDs)
		if len(artists) == 0 {
			artists = albumArtists
		}
		for _, id := range artists {
			album := strings.TrimSpace(track.AlbumID)
			if album == "" {
				album = "unknown-album:" + id
			}
			assocSet[ArtistAlbumAssociation{ArtistID: id, AlbumID: album, Role: "track_artist"}] = struct{}{}
			if byArtist[id] == nil {
				byArtist[id] = albumGenres{}
			}
			if byArtist[id][album] == nil {
				byArtist[id][album] = map[string]struct{}{}
			}
			for _, genre := range genres {
				byArtist[id][album][genre] = struct{}{}
				genreSet[genre] = struct{}{}
			}
		}
		album := strings.TrimSpace(track.AlbumID)
		for _, id := range albumArtists {
			key := album
			if key == "" {
				key = "unknown-album:" + id
			}
			assocSet[ArtistAlbumAssociation{ArtistID: id, AlbumID: key, Role: "album_artist"}] = struct{}{}
		}
	}

	vocabulary := sortedKeys(genreSet)
	artistIDs := sortedKeys(byArtist)
	genreColumn := make(map[string]int, len(vocabulary))
	for i, genre := range vocabulary {
		genreColumn[genre] = i
	}
	// Each artist can be assembled independently. Results are written to fixed
	// canonical slots, so scheduling and worker count cannot affect them.
	rawRows := make([][]SparseValue, len(artistIDs))
	jobs := make(chan int)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for range min(workers, max(1, len(artistIDs))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				weights := map[int]float64{}
				albums := byArtist[artistIDs[index]]
				albumIDs := sortedKeys(albums)
				for _, album := range albumIDs {
					genres := sortedKeys(albums[album])
					if len(genres) == 0 {
						continue
					}
					weight := 1 / float64(len(genres))
					for _, genre := range genres {
						weights[genreColumn[genre]] += weight
					}
				}
				columns := make([]int, 0, len(weights))
				for column := range weights {
					columns = append(columns, column)
				}
				sort.Ints(columns)
				row := make([]SparseValue, 0, len(columns))
				for _, column := range columns {
					row = append(row, SparseValue{Column: column, Value: weights[column]})
				}
				rawRows[index] = row
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range artistIDs {
			select {
			case jobs <- i:
			case <-ctx.Done():
				errMu.Lock()
				firstErr = ctx.Err()
				errMu.Unlock()
				return
			}
		}
	}()
	wg.Wait()
	if firstErr != nil {
		return MetadataModel{}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return MetadataModel{}, err
	}

	df := make([]int, len(vocabulary))
	for _, row := range rawRows {
		for _, value := range row {
			df[value.Column]++
		}
	}
	idf := make([]float64, len(vocabulary))
	for column := range idf {
		idf[column] = math.Log((1+float64(len(artistIDs)))/(1+float64(df[column]))) + 1
	}
	rows := make([]SparseRow, len(artistIDs))
	for i, raw := range rawRows {
		var total float64
		for _, value := range raw {
			total += value.Value
		}
		var normSquared float64
		for j := range raw {
			if total > 0 {
				raw[j].Value = raw[j].Value / total * idf[raw[j].Column]
			}
			normSquared += raw[j].Value * raw[j].Value
		}
		if normSquared > 0 {
			scale := 1 / math.Sqrt(normSquared)
			for j := range raw {
				raw[j].Value *= scale
			}
		}
		rows[i] = SparseRow{ArtistID: artistIDs[i], Values: raw}
	}
	associations := make([]ArtistAlbumAssociation, 0, len(assocSet))
	for association := range assocSet {
		associations = append(associations, association)
	}
	sort.Slice(associations, func(i, j int) bool {
		a, b := associations[i], associations[j]
		if a.ArtistID != b.ArtistID {
			return a.ArtistID < b.ArtistID
		}
		if a.AlbumID != b.AlbumID {
			return a.AlbumID < b.AlbumID
		}
		return a.Role < b.Role
	})
	model := MetadataModel{Version: MetadataVersion, Vocabulary: vocabulary, IDF: idf, Rows: rows, Associations: associations}
	model.SVD = fitSparseSVD(ctx, rows, len(vocabulary), options)
	if err := ctx.Err(); err != nil {
		return MetadataModel{}, err
	}
	return model, nil
}

func canonicalIDs(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func canonicalStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = cases.Fold().String(norm.NFC.String(strings.TrimSpace(value)))
		value = strings.Join(strings.Fields(value), " ")
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
