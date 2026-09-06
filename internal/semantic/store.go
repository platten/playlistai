// Package semantic loads an optional, versioned semantic-feature sidecar. It
// never contacts metadata services; sidecars are built explicitly offline.
package semantic

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const DBFile = "semantic.sqlite"

const QueryEncoderPrecomputed = "precomputed-query-v1"

type Store struct {
	db         *sql.DB
	catalog    ports.Catalog
	info       core.FeatureStoreInfo
	queryReady bool
}

func Open(path, catalogVersion string, catalog ports.Catalog) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, catalog: catalog}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: %w", err)
	}
	if err := store.loadInfo(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if store.info.SchemaVersion < 1 || store.info.SchemaVersion > core.CurrentFeatureSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: schema %d is incompatible with supported versions 1..%d", store.info.SchemaVersion, core.CurrentFeatureSchemaVersion)
	}
	if catalogVersion != "" && store.info.CatalogVersion != catalogVersion {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: catalog version %q does not match %q", store.info.CatalogVersion, catalogVersion)
	}
	return store, nil
}

func (s *Store) Close() error                { return s.db.Close() }
func (s *Store) Info() core.FeatureStoreInfo { return s.info }

// SearchReady reports whether the sidecar embeds a query vocabulary that the
// Go runtime can use without Python or another model runtime. Schema-v1
// sidecars remain readable as feature stores but cannot perform retrieval.
func (s *Store) SearchReady() bool { return s.queryReady }

func (s *Store) loadInfo() error {
	rows, err := s.db.Query("SELECT key, value FROM meta")
	if err != nil {
		return fmt.Errorf("semantic sidecar metadata: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		values[key] = value
	}
	parseInt := func(key string) (int, error) {
		value, err := strconv.Atoi(values[key])
		if err != nil {
			return 0, fmt.Errorf("semantic sidecar: invalid %s", key)
		}
		return value, nil
	}
	var parseErr error
	if s.info.SchemaVersion, parseErr = parseInt("schema_version"); parseErr != nil {
		return parseErr
	}
	if s.info.EmbeddingDim, parseErr = parseInt("embedding_dim"); parseErr != nil {
		return parseErr
	}
	if s.info.TrackCount, parseErr = parseInt("track_count"); parseErr != nil {
		return parseErr
	}
	s.info.CatalogVersion = values["catalog_version"]
	s.info.FeatureVersion = values["feature_version"]
	s.info.TextModel = values["text_model"]
	s.info.ModelRevision = values["model_revision"]
	if s.info.CatalogVersion == "" || s.info.FeatureVersion == "" || s.info.TextModel == "" || s.info.ModelRevision == "" || s.info.EmbeddingDim <= 0 {
		return errors.New("semantic sidecar: incomplete version metadata")
	}
	if facets := values["supported_facets"]; facets != "" {
		if err := json.Unmarshal([]byte(facets), &s.info.SupportedFacets); err != nil {
			return fmt.Errorf("semantic sidecar: supported facets: %w", err)
		}
	}
	var featureCount, vectorCount int
	if err := s.db.QueryRow("SELECT count(*) FROM features").Scan(&featureCount); err != nil {
		return err
	}
	if err := s.db.QueryRow("SELECT count(*) FROM semantic_vectors").Scan(&vectorCount); err != nil {
		return err
	}
	if featureCount != s.info.TrackCount || vectorCount != s.info.TrackCount {
		return fmt.Errorf("semantic sidecar: declared %d tracks but found %d feature rows and %d vectors", s.info.TrackCount, featureCount, vectorCount)
	}
	if s.info.SchemaVersion >= 2 {
		s.info.QueryEncoder = values["query_encoder"]
		if s.info.QueryEncoder != QueryEncoderPrecomputed {
			return fmt.Errorf("semantic sidecar: unsupported query encoder %q", s.info.QueryEncoder)
		}
		if s.info.QueryTermCount, parseErr = parseInt("query_term_count"); parseErr != nil || s.info.QueryTermCount <= 0 {
			return errors.New("semantic sidecar: invalid query_term_count")
		}
		var queryCount int
		if err := s.db.QueryRow("SELECT count(*) FROM query_vectors").Scan(&queryCount); err != nil {
			return fmt.Errorf("semantic sidecar query vocabulary: %w", err)
		}
		if queryCount != s.info.QueryTermCount {
			return fmt.Errorf("semantic sidecar: declared %d query terms but found %d", s.info.QueryTermCount, queryCount)
		}
		s.queryReady = true
	}
	return rows.Err()
}

func (s *Store) Features(ctx context.Context, trackID string) (core.TrackFeatures, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT feature_json FROM features WHERE track_id = ?", trackID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.TrackFeatures{}, false, nil
	}
	if err != nil {
		return core.TrackFeatures{}, false, err
	}
	var feature core.TrackFeatures
	if err := json.Unmarshal([]byte(raw), &feature); err != nil {
		return core.TrackFeatures{}, false, fmt.Errorf("semantic sidecar: decode features for %s: %w", trackID, err)
	}
	if feature.SchemaVersion != s.info.SchemaVersion || feature.TrackID != trackID || feature.CatalogVersion != s.info.CatalogVersion {
		return core.TrackFeatures{}, false, fmt.Errorf("semantic sidecar: incompatible feature row %s", trackID)
	}
	if err := validateFeature(feature); err != nil {
		return core.TrackFeatures{}, false, fmt.Errorf("semantic sidecar: feature row %s: %w", trackID, err)
	}
	return feature, true, nil
}

func validateFeature(feature core.TrackFeatures) error {
	values := []core.FeatureValue{feature.ArtistID, feature.RecordingID, feature.VocalEvidence, feature.ReleaseDates.OriginalEdition, feature.ReleaseDates.ReleaseEdition}
	values = append(values, feature.Tags...)
	values = append(values, feature.Descriptions...)
	values = append(values, feature.Styles...)
	values = append(values, feature.Moods...)
	values = append(values, feature.Instrumentation...)
	for _, value := range values {
		switch value.Missingness {
		case core.FeatureKnown:
			if strings.TrimSpace(value.Value) == "" || value.Confidence < 0 || value.Confidence > 1 || len(value.Provenance) == 0 {
				return errors.New("known value requires text, confidence in [0,1], and provenance")
			}
		case core.FeatureUnknown, "":
			if strings.TrimSpace(value.Value) != "" {
				return errors.New("unknown value contains text")
			}
		default:
			return fmt.Errorf("invalid missingness %q", value.Missingness)
		}
	}
	if feature.VocalEvidence.Missingness == core.FeatureKnown {
		switch strings.ToLower(feature.VocalEvidence.Value) {
		case "vocal", "instrumental", "mixed":
		default:
			return fmt.Errorf("invalid vocal evidence %q", feature.VocalEvidence.Value)
		}
	}
	if feature.Preview.Available {
		if feature.Preview.StartSeconds < 0 || feature.Preview.EndSeconds <= feature.Preview.StartSeconds || feature.Preview.CoveredSeconds <= 0 || feature.Preview.CoveredSeconds > feature.Preview.EndSeconds-feature.Preview.StartSeconds {
			return errors.New("invalid preview segment coverage")
		}
	}
	return nil
}

func (s *Store) Search(ctx context.Context, text string, limit int) ([]core.SemanticHit, error) {
	if strings.TrimSpace(text) == "" || limit <= 0 {
		return []core.SemanticHit{}, nil
	}
	if !s.queryReady {
		return nil, fmt.Errorf("semantic search: %w: sidecar has no embedded Go query vocabulary", core.ErrUnavailable)
	}
	query, err := s.queryVector(ctx, text)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT track_id, embedding FROM semantic_vectors ORDER BY track_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := make([]core.SemanticHit, 0, min(limit, s.info.TrackCount))
	seen := 0
	for rows.Next() {
		if seen&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		seen++
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if s.catalog != nil {
			if _, ok := s.catalog.RowOf(id); !ok {
				continue
			}
		}
		vector, err := decodeVector(blob, s.info.EmbeddingDim)
		if err != nil {
			return nil, fmt.Errorf("semantic sidecar: track %s: %w", id, err)
		}
		hit := core.SemanticHit{TrackID: id, Score: dot(query, vector)}
		if len(hits) < limit {
			hits = append(hits, hit)
			sortHits(hits)
		} else if better(hit, hits[len(hits)-1]) {
			hits[len(hits)-1] = hit
			sortHits(hits)
		}
	}
	return hits, rows.Err()
}

func (s *Store) queryVector(ctx context.Context, text string) ([]float32, error) {
	keys := queryKeys(text)
	if len(keys) == 0 {
		return nil, fmt.Errorf("semantic search: %w: query has no searchable terms", core.ErrUnavailable)
	}
	// A full-phrase entry is an actual encode_query result and is preferable to
	// composing its component terms. The remaining entries provide bounded
	// vocabulary fallback for phrases not seen by the offline builder.
	if len(keys) > 1 {
		if vector, ok, err := s.lookupQueryVector(ctx, keys[0]); err != nil {
			return nil, err
		} else if ok {
			return vector, nil
		}
		keys = keys[1:]
	}
	query := make([]float32, s.info.EmbeddingDim)
	matched := 0
	for _, key := range keys {
		vector, ok, err := s.lookupQueryVector(ctx, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		matched++
		for i, value := range vector {
			query[i] += value
		}
	}
	if matched == 0 {
		return nil, fmt.Errorf("semantic search: %w: query is outside the sidecar vocabulary", core.ErrUnavailable)
	}
	normalize(query)
	return query, nil
}

func (s *Store) lookupQueryVector(ctx context.Context, key string) ([]float32, bool, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, "SELECT embedding FROM query_vectors WHERE term = ?", key).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	vector, err := decodeVector(blob, s.info.EmbeddingDim)
	if err != nil {
		return nil, false, fmt.Errorf("semantic sidecar query term %q: %w", key, err)
	}
	return vector, true, nil
}

func queryKeys(text string) []string {
	tokens := semanticTokens(text)
	if len(tokens) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	add := func(out []string, value string) []string {
		if _, ok := seen[value]; ok {
			return out
		}
		seen[value] = struct{}{}
		return append(out, value)
	}
	var result []string
	if len(tokens) > 1 {
		result = add(result, strings.Join(tokens, " "))
		for i := 0; i+1 < len(tokens); i++ {
			result = add(result, tokens[i]+" "+tokens[i+1])
		}
	}
	for _, token := range tokens {
		result = add(result, token)
	}
	return result
}

func semanticTokens(text string) []string {
	text = strings.ToLower(norm.NFKC.String(text))
	var tokens []string
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		value := token.String()
		token.Reset()
		if _, skip := semanticStopWords[value]; !skip {
			tokens = append(tokens, value)
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

var semanticStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "but": {}, "for": {}, "in": {}, "of": {}, "or": {}, "the": {}, "to": {}, "with": {},
}

func decodeVector(blob []byte, dim int) ([]float32, error) {
	if len(blob) != 4*dim {
		return nil, fmt.Errorf("embedding has %d bytes, want %d", len(blob), 4*dim)
	}
	result := make([]float32, dim)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return result, nil
}

func normalize(vector []float32) {
	var sum float64
	for _, value := range vector {
		sum += float64(value * value)
	}
	if sum == 0 {
		return
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= scale
	}
}

func dot(left, right []float32) float64 {
	var value float64
	for i := range left {
		value += float64(left[i] * right[i])
	}
	return value
}

func better(left, right core.SemanticHit) bool {
	return left.Score > right.Score || left.Score == right.Score && left.TrackID < right.TrackID
}
func sortHits(hits []core.SemanticHit) {
	sort.SliceStable(hits, func(i, j int) bool { return better(hits[i], hits[j]) })
}

var _ ports.FeatureStore = (*Store)(nil)
var _ ports.SemanticSearcher = (*Store)(nil)
