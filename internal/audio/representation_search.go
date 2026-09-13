package audio

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const representationSearchVersion = "pooled-exact/v1"
const representationBackfillBatch = 32
const maxRepresentationSearchResults = 512
const maxRepresentationSearchQueries = 64
const maxRepresentationBatchResults = 1024

// The projection is disposable. Source updates invalidate it in the same
// transaction, including writes performed by an importer instead of Put.
func initRepresentationSearchSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS audio_representation_vector (
 id TEXT PRIMARY KEY, valid INTEGER NOT NULL CHECK(valid IN (0,1)),
 analyzed TEXT NOT NULL, pooled BLOB, digest TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS audio_representation_search
 ON audio_representation(catalog, model, track, track_key);
CREATE TRIGGER IF NOT EXISTS audio_representation_vector_insert
 AFTER INSERT ON audio_representation BEGIN
 DELETE FROM audio_representation_vector WHERE id=NEW.id; END;
CREATE TRIGGER IF NOT EXISTS audio_representation_vector_delete
 AFTER DELETE ON audio_representation BEGIN
 DELETE FROM audio_representation_vector WHERE id=OLD.id; END;
CREATE TRIGGER IF NOT EXISTS audio_representation_vector_update
 AFTER UPDATE ON audio_representation BEGIN
 DELETE FROM audio_representation_vector WHERE id=OLD.id OR id=NEW.id; END;`)
	return err
}

func putRepresentationProjection(ctx context.Context, tx *sql.Tx, a core.AudioRepresentation) error {
	stamp, encoded, err := encodeRepresentationProjection(a)
	if err != nil {
		return err
	}
	digest := representationProjectionDigest(a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), stamp, encoded)
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO audio_representation_vector
 (id,valid,analyzed,pooled,digest) VALUES(?,1,?,?,?)`, a.ID, stamp, encoded, digest)
	return err
}

func encodeRepresentationProjection(a core.AudioRepresentation) (string, []byte, error) {
	encoded := make([]byte, 4*len(a.Pooled))
	for i, value := range a.Pooled {
		binary.LittleEndian.PutUint32(encoded[4*i:], math.Float32bits(value))
	}
	analyzed, err := time.Parse(time.RFC3339Nano, a.AnalyzedAt)
	if err != nil {
		return "", nil, err
	}
	// Fixed fractional precision preserves chronological string ordering.
	stamp := analyzed.UTC().Format("2006-01-02T15:04:05.000000000Z")
	return stamp, encoded, nil
}

func representationProjectionDigest(id, catalog, track, key, model, analyzed string, pooled []byte) string {
	h := sha256.New()
	for _, value := range []string{representationSearchVersion, id, catalog, track, key, model, analyzed} {
		writeRepresentationHashPart(h, []byte(value))
	}
	writeRepresentationHashPart(h, pooled)
	return hex.EncodeToString(h.Sum(nil))
}

func writeRepresentationHashPart(h hash.Hash, value []byte) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

// Backfill validates small batches of legacy records once. Invalid rows receive
// an unavailable marker so subsequent searches do not keep reparsing them. A
// corrected source row invalidates that marker through the update trigger.
func (s *RepresentationStore) backfillRepresentationSearch(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		complete, err := s.backfillRepresentationBatch(ctx)
		if err != nil || complete {
			return err
		}
	}
}

func (s *RepresentationStore) backfillRepresentationBatch(ctx context.Context) (bool, error) {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.catalog,a.track,a.track_key,a.model,a.data
 FROM audio_representation a LEFT JOIN audio_representation_vector p ON p.id=a.id
 WHERE p.id IS NULL ORDER BY a.rowid LIMIT ?`, representationBackfillBatch)
	if err != nil {
		return false, err
	}
	type source struct{ id, catalog, track, key, model, data string }
	batch := make([]source, 0, representationBackfillBatch)
	for rows.Next() {
		var row source
		if err := rows.Scan(&row.id, &row.catalog, &row.track, &row.key, &row.model, &row.data); err != nil {
			_ = rows.Close()
			return false, err
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, row := range batch {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var a core.AudioRepresentation
		valid := json.Unmarshal([]byte(row.data), &a) == nil && validateRepresentation(a) == nil &&
			a.ID == row.id && a.CatalogVersion == row.catalog && a.TrackID == row.track &&
			a.TrackKey == row.key && Fingerprint(a.Model) == row.model
		if valid {
			err = putRepresentationProjection(ctx, tx, a)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO audio_representation_vector
 (id,valid,analyzed,pooled,digest) VALUES(?,0,'',NULL,'')`, row.id)
		}
		if err != nil {
			return false, err
		}
	}
	return len(batch) < representationBackfillBatch, tx.Commit()
}

// Each track has one newest compatible record. Recording aliases then share one
// result, with timestamps and stable IDs deciding ties independently of insertion
// order. Only compact pooled vectors are streamed; full JSON is read for top hits.
const representationSearchSQL = `WITH current_track AS (
 SELECT a.id,a.track,a.track_key,p.analyzed,
 ROW_NUMBER() OVER (PARTITION BY a.track ORDER BY p.analyzed DESC,a.id) AS track_rank
 FROM audio_representation a JOIN audio_representation_vector p ON p.id=a.id
 WHERE a.catalog=? AND a.model=? AND p.valid=1
), current_recording AS (
 SELECT *,ROW_NUMBER() OVER (PARTITION BY track_key ORDER BY analyzed DESC,track,id) AS recording_rank
 FROM current_track WHERE track_rank=1
)
SELECT c.id,c.track,c.track_key,c.analyzed,p.pooled,p.digest
 FROM current_recording c JOIN audio_representation_vector p ON p.id=c.id
 WHERE c.recording_rank=1`

// Search returns exact cosine neighbors from one stable SQLite read view. Its
// memory is bounded by one vector, a top-K heap and the returned representations.
// Query limits above 512 are clamped. Coverage counts compatible recordings
// before request-local exclusions. Limit zero performs no similarity scoring.
func (s *RepresentationStore) Search(ctx context.Context, query ports.AudioRepresentationQuery) (core.AudioRepresentationSearchResult, error) {
	results, err := s.SearchBatch(ctx, []ports.AudioRepresentationQuery{query})
	if err != nil {
		return core.AudioRepresentationSearchResult{}, err
	}
	return results[0], nil
}

type representationSearchState struct {
	query        ports.AudioRepresentationQuery
	norm         float64
	limit        int
	excludedKeys map[string]bool
	best         representationHitHeap
}

// SearchBatch scans one stable compatible SQLite view for at most 64 queries.
// Per-query limits are capped at 512, with an aggregate capacity of 1024 hits.
// These bounds apply before scanning, including duplicate hits across queries.
func (s *RepresentationStore) SearchBatch(ctx context.Context, queries []ports.AudioRepresentationQuery) ([]core.AudioRepresentationSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	states, err := prepareRepresentationQueries(queries)
	if err != nil {
		return nil, err
	}
	query := queries[0]
	if err := s.backfillRepresentationSearch(ctx); err != nil {
		return nil, err
	}
	tx, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	model := Fingerprint(query.Model)
	for i := range states {
		states[i].excludedKeys, err = representationExcludedKeys(ctx, tx, states[i].query, model)
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, representationSearchSQL, query.CatalogVersion, model)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, part := range []string{representationSearchVersion, query.CatalogVersion, model} {
		writeRepresentationHashPart(h, []byte(part))
	}
	var searchable int64
	// The metadata window queries may choose any row order. Fold unique record
	// hashes commutatively so the view is stable without sorting/copying all BLOBs.
	var members [sha256.Size]byte
	vector := make([]float32, query.Model.Dimension)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var hit representationHit
		var stamp string
		var pooled []byte
		if err := rows.Scan(&hit.id, &hit.track, &hit.key, &stamp, &pooled, &hit.digest); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if hit.digest != representationProjectionDigest(hit.id, query.CatalogVersion, hit.track, hit.key, model, stamp, pooled) {
			continue
		}
		norm, valid := decodeRepresentationPooled(pooled, vector)
		if !valid {
			continue
		}
		searchable++
		member := sha256.Sum256([]byte(hit.id + "\x00" + hit.digest))
		for i := range members {
			members[i] ^= member[i]
		}
		for i := range states {
			state := &states[i]
			if state.limit == 0 {
				continue
			}
			if _, excluded := state.query.Exclude[hit.track]; excluded || state.excludedKeys[hit.key] {
				continue
			}
			dot := 0.0
			for j, value := range vector {
				dot += float64(value) * float64(state.query.Vector[j])
			}
			hit.score = max(-1, min(1, dot/(norm*state.norm)))
			if state.best.Len() < state.limit {
				heap.Push(&state.best, hit)
			} else if representationHitBetter(hit, state.best[0]) {
				state.best[0] = hit
				heap.Fix(&state.best, 0)
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	writeRepresentationHashPart(h, members[:])
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(searchable))
	writeRepresentationHashPart(h, count[:])
	fingerprint := hex.EncodeToString(h.Sum(nil))
	results, err := loadRepresentationMatches(ctx, tx, states, searchable, fingerprint)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func prepareRepresentationQueries(queries []ports.AudioRepresentationQuery) ([]representationSearchState, error) {
	if len(queries) == 0 || len(queries) > maxRepresentationSearchQueries {
		return nil, fmt.Errorf("audio: representation search requires 1 to %d queries", maxRepresentationSearchQueries)
	}
	states := make([]representationSearchState, len(queries))
	total := 0
	for i, query := range queries {
		if query.CatalogVersion == "" || !validRepresentationSearchModel(query.Model) || query.Limit < 0 {
			return nil, fmt.Errorf("audio: invalid representation search identity or limit")
		}
		if query.Model != queries[0].Model || query.CatalogVersion != queries[0].CatalogVersion {
			return nil, fmt.Errorf("audio: representation search batch requires one catalog and model identity")
		}
		states[i] = representationSearchState{query: query, limit: min(query.Limit, maxRepresentationSearchResults)}
		if query.Limit > 0 {
			var valid bool
			states[i].norm, valid = representationQueryNorm(query.Vector, query.Model.Dimension)
			if !valid {
				return nil, fmt.Errorf("audio: invalid representation search vector")
			}
		}
		total += states[i].limit
	}
	if total > maxRepresentationBatchResults {
		return nil, fmt.Errorf("audio: representation batch exceeds %d total requested hits", maxRepresentationBatchResults)
	}
	return states, nil
}

func loadRepresentationMatches(ctx context.Context, tx *sql.Tx, states []representationSearchState, searchable int64, fingerprint string) ([]core.AudioRepresentationSearchResult, error) {
	results := make([]core.AudioRepresentationSearchResult, len(states))
	for i, state := range states {
		result := &results[i]
		result.SearchableTracks, result.Fingerprint = searchable, fingerprint
		result.Matches = make([]core.AudioRepresentationMatch, 0, len(state.best))
		sort.Slice(state.best, func(i, j int) bool { return representationHitBetter(state.best[i], state.best[j]) })
		for _, hit := range state.best {
			var raw string
			if err := tx.QueryRowContext(ctx, `SELECT data FROM audio_representation WHERE id=?`, hit.id).Scan(&raw); err != nil {
				return nil, err
			}
			var a core.AudioRepresentation
			if json.Unmarshal([]byte(raw), &a) != nil || validateRepresentation(a) != nil ||
				a.ID != hit.id || a.TrackID != hit.track || a.TrackKey != hit.key ||
				a.CatalogVersion != state.query.CatalogVersion || a.Model != state.query.Model {
				return nil, fmt.Errorf("audio: representation changed outside its validated search projection")
			}
			stamp, pooled, err := encodeRepresentationProjection(a)
			if err != nil || representationProjectionDigest(a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), stamp, pooled) != hit.digest {
				return nil, fmt.Errorf("audio: representation search vector differs from authoritative evidence")
			}
			result.Matches = append(result.Matches, core.AudioRepresentationMatch{Representation: a, Score: hit.score})
		}
	}
	return results, nil
}

func validRepresentationSearchModel(m core.AudioRepresentationIdentity) bool {
	return m.Model != "" && m.Revision != "" && m.Preprocessing != "" && m.Runtime != "" &&
		m.Pooling != "" && m.Dimension > 0 && m.Dimension <= 8192 && representationHash(m.WeightsSHA256)
}

func representationQueryNorm(vector []float32, dimension int) (float64, bool) {
	if len(vector) != dimension {
		return 0, false
	}
	norm := 0.0
	for _, v := range vector {
		norm += float64(v) * float64(v)
	}
	return math.Sqrt(norm), norm > 0 && !math.IsNaN(norm) && !math.IsInf(norm, 0)
}

func decodeRepresentationPooled(pooled []byte, vector []float32) (float64, bool) {
	if len(pooled) != 4*len(vector) {
		return 0, false
	}
	norm := 0.0
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(pooled[4*i:]))
		norm += float64(vector[i]) * float64(vector[i])
	}
	if !(norm > 0.9 && norm < 1.1) {
		return 0, false
	}
	return math.Sqrt(norm), true
}

func representationExcludedKeys(ctx context.Context, tx *sql.Tx, query ports.AudioRepresentationQuery, model string) (map[string]bool, error) {
	keys := map[string]bool{}
	for id := range query.Exclude {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT track_key FROM audio_representation
 WHERE catalog=? AND model=? AND track=?`, query.CatalogVersion, model, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				_ = rows.Close()
				return nil, err
			}
			keys[key] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

type representationHit struct {
	id, track, key, digest string
	score                  float64
}

func representationHitBetter(a, b representationHit) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.track != b.track {
		return a.track < b.track
	}
	return a.id < b.id
}

type representationHitHeap []representationHit

func (h representationHitHeap) Len() int           { return len(h) }
func (h representationHitHeap) Less(i, j int) bool { return representationHitBetter(h[j], h[i]) }
func (h representationHitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *representationHitHeap) Push(value any)    { *h = append(*h, value.(representationHit)) }
func (h *representationHitHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

var _ ports.AudioRepresentationSearcher = (*RepresentationStore)(nil)
var _ ports.AudioRepresentationBatchSearcher = (*RepresentationStore)(nil)
