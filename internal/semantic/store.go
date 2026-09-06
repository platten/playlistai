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

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const DBFile = "semantic.sqlite"

type Store struct {
	db      *sql.DB
	catalog ports.Catalog
	encoder ports.TextEmbedder
	info    core.FeatureStoreInfo
}

func Open(path, catalogVersion string, catalog ports.Catalog, encoder ports.TextEmbedder) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, catalog: catalog, encoder: encoder}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: %w", err)
	}
	if err := store.loadInfo(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if store.info.SchemaVersion != core.CurrentFeatureSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: schema %d is incompatible with %d", store.info.SchemaVersion, core.CurrentFeatureSchemaVersion)
	}
	if catalogVersion != "" && store.info.CatalogVersion != catalogVersion {
		_ = db.Close()
		return nil, fmt.Errorf("semantic sidecar: catalog version %q does not match %q", store.info.CatalogVersion, catalogVersion)
	}
	if encoder != nil {
		name, revision, dim := encoder.Model()
		if name != store.info.TextModel || revision != store.info.ModelRevision || dim != store.info.EmbeddingDim {
			_ = db.Close()
			return nil, fmt.Errorf("semantic sidecar: encoder %s@%s/%d does not match index %s@%s/%d", name, revision, dim, store.info.TextModel, store.info.ModelRevision, store.info.EmbeddingDim)
		}
	}
	return store, nil
}

func (s *Store) Close() error                { return s.db.Close() }
func (s *Store) Info() core.FeatureStoreInfo { return s.info }

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
	if feature.SchemaVersion != core.CurrentFeatureSchemaVersion || feature.TrackID != trackID || feature.CatalogVersion != s.info.CatalogVersion {
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
	if s.encoder == nil {
		return nil, fmt.Errorf("semantic search: %w: compatible local text encoder is not configured", core.ErrUnavailable)
	}
	if strings.TrimSpace(text) == "" || limit <= 0 {
		return []core.SemanticHit{}, nil
	}
	query, err := s.encoder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(query) != s.info.EmbeddingDim {
		return nil, fmt.Errorf("semantic search: query dimension %d, want %d", len(query), s.info.EmbeddingDim)
	}
	normalize(query)
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
