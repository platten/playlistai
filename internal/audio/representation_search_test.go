package audio

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func searchRepresentation(id, key string, angle float64) core.AudioRepresentation {
	a := storedRepresentation()
	a.TrackID, a.TrackKey = id, key
	a.Pooled = []float32{float32(math.Cos(angle)), float32(math.Sin(angle))}
	for i := range a.Segments {
		a.Segments[i].Vector = append([]float32(nil), a.Pooled...)
	}
	a.ID = ""
	a.ID = Fingerprint(a)
	return a
}

func openRepresentationSearchStore(t *testing.T) *RepresentationStore {
	t.Helper()
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	return parent.Representations()
}

func searchQuery(limit int) ports.AudioRepresentationQuery {
	a := storedRepresentation()
	return ports.AudioRepresentationQuery{CatalogVersion: a.CatalogVersion, Model: a.Model, Vector: []float32{1, 0}, Limit: limit}
}

func putSearchRepresentation(t *testing.T, s *RepresentationStore, a core.AudioRepresentation) {
	t.Helper()
	if err := s.Put(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func TestRepresentationSearchMatchesExactOracle(t *testing.T) {
	s := openRepresentationSearchStore(t)
	rng := rand.New(rand.NewSource(42))
	query := searchQuery(17)
	query.Vector = []float32{4, -3} // Search accepts nonunit finite reference vectors.
	var want []core.AudioRepresentationMatch
	for i := range 150 {
		a := searchRepresentation(fmt.Sprintf("t-%03d", i), fmt.Sprintf("r-%03d", i), rng.Float64()*2*math.Pi)
		putSearchRepresentation(t, s, a)
		x, y := float64(a.Pooled[0]), float64(a.Pooled[1])
		want = append(want, core.AudioRepresentationMatch{Representation: a, Score: (4*x - 3*y) / (5 * math.Sqrt(x*x+y*y))})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Score > want[j].Score })
	got, err := s.Search(context.Background(), query)
	if err != nil || got.SearchableTracks != 150 || len(got.Matches) != query.Limit || got.Fingerprint == "" {
		t.Fatalf("search: %+v, %v", got, err)
	}
	for i, hit := range got.Matches {
		if !reflect.DeepEqual(hit.Representation, want[i].Representation) || math.Abs(hit.Score-want[i].Score) > 1e-12 {
			t.Fatalf("oracle rank %d: %+v != %+v", i, hit, want[i])
		}
	}
	got.Matches[0].Representation.Pooled[0] = 0
	again, err := s.Search(context.Background(), query)
	if err != nil || !reflect.DeepEqual(again.Matches[0].Representation, want[0].Representation) {
		t.Fatalf("caller changed stored/search data: %v", err)
	}
	query.Limit, query.Vector = 0, nil
	coverage, err := s.Search(context.Background(), query)
	if err != nil || coverage.SearchableTracks != 150 || len(coverage.Matches) != 0 || coverage.Fingerprint != got.Fingerprint {
		t.Fatalf("coverage differs from scored view: %+v %v", coverage, err)
	}
}

func TestRepresentationSearchCurrentRecordingTiesAndExclusions(t *testing.T) {
	a := searchRepresentation("a", "same-recording", 0)
	b := searchRepresentation("b", "same-recording", 0)
	old := searchRepresentation("c", "old-key", 0)
	current := searchRepresentation("c", "current-key", math.Pi/2)
	current.AnalyzedAt = "2026-09-11T12:00:00.1Z"
	current.ID = ""
	current.ID = Fingerprint(current)
	d := searchRepresentation("d", "different-recording", 0)
	input := []core.AudioRepresentation{a, b, old, current, d}
	var first core.AudioRepresentationSearchResult
	for _, reverse := range []bool{false, true} {
		s := openRepresentationSearchStore(t)
		for i := range input {
			if reverse {
				i = len(input) - 1 - i
			}
			putSearchRepresentation(t, s, input[i])
		}
		got, err := s.Search(context.Background(), searchQuery(10))
		if err != nil || got.SearchableTracks != 3 || len(got.Matches) != 3 {
			t.Fatalf("duplicate/current records: %+v %v", got, err)
		}
		ids := []string{got.Matches[0].Representation.TrackID, got.Matches[1].Representation.TrackID, got.Matches[2].Representation.TrackID}
		if !reflect.DeepEqual(ids, []string{"a", "d", "c"}) || got.Matches[2].Representation.ID != current.ID {
			t.Fatalf("stable tie/current record: %v", ids)
		}
		if reverse && !reflect.DeepEqual(first, got) {
			t.Fatal("insertion order changed neighbors or view fingerprint")
		}
		first = got
		query := searchQuery(10)
		query.Exclude = map[string]struct{}{"b": {}}
		excluded, err := s.Search(context.Background(), query)
		if err != nil || excluded.SearchableTracks != 3 || len(excluded.Matches) != 2 || excluded.Matches[0].Representation.TrackID != "d" || excluded.Fingerprint != got.Fingerprint {
			t.Fatalf("excluded recording alias leaked: %+v %v", excluded, err)
		}
	}
}

func TestRepresentationSearchIdentityAndInvalidQuery(t *testing.T) {
	s := openRepresentationSearchStore(t)
	putSearchRepresentation(t, s, storedRepresentation())
	for _, change := range []func(*ports.AudioRepresentationQuery){
		func(q *ports.AudioRepresentationQuery) { q.CatalogVersion += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.Model += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.Revision += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.Preprocessing += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.Runtime += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.Pooling += "x" },
		func(q *ports.AudioRepresentationQuery) { q.Model.WeightsSHA256 = strings.Repeat("f", 64) },
		func(q *ports.AudioRepresentationQuery) { q.Model.Dimension = 3; q.Vector = []float32{1, 0, 0} },
	} {
		query := searchQuery(5)
		change(&query)
		got, err := s.Search(context.Background(), query)
		if err != nil || got.SearchableTracks != 0 || len(got.Matches) != 0 {
			t.Fatalf("incompatible view returned data: %+v %v", got, err)
		}
	}
	for _, change := range []func(*ports.AudioRepresentationQuery){
		func(q *ports.AudioRepresentationQuery) { q.Vector = nil },
		func(q *ports.AudioRepresentationQuery) { q.Vector = []float32{0, 0} },
		func(q *ports.AudioRepresentationQuery) { q.Vector[0] = float32(math.NaN()) },
		func(q *ports.AudioRepresentationQuery) { q.Vector[0] = float32(math.Inf(1)) },
		func(q *ports.AudioRepresentationQuery) { q.CatalogVersion = "" },
		func(q *ports.AudioRepresentationQuery) { q.Limit = -1 },
		func(q *ports.AudioRepresentationQuery) { q.Model.WeightsSHA256 = "bad" },
	} {
		query := searchQuery(5)
		change(&query)
		if _, err := s.Search(context.Background(), query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
}

func insertLegacyRepresentation(t *testing.T, s *RepresentationStore, a core.AudioRepresentation) {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`INSERT INTO audio_representation(id,catalog,track,track_key,model,data)
 VALUES(?,?,?,?,?,?)`, a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestRepresentationSearchBackfillResumesAndInvalidates(t *testing.T) {
	s := openRepresentationSearchStore(t)
	for i := range representationBackfillBatch + 7 {
		a := searchRepresentation(fmt.Sprint(i), fmt.Sprint(i), 0)
		insertLegacyRepresentation(t, s, a)
	}
	bad := searchRepresentation("bad", "bad", 0)
	bad.Pooled = []float32{0, 0} // Keep its original fingerprint: source corruption.
	insertLegacyRepresentation(t, s, bad)
	complete, err := s.backfillRepresentationBatch(context.Background())
	if err != nil || complete {
		t.Fatalf("first bounded batch: %v %v", complete, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.backfillRepresentationBatch(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch: %v", err)
	}
	var count int
	if err := s.store.db.QueryRow("SELECT COUNT(*) FROM audio_representation_vector").Scan(&count); err != nil || count != representationBackfillBatch {
		t.Fatalf("completed batch was not retained: %d %v", count, err)
	}
	got, err := s.Search(context.Background(), searchQuery(100))
	if err != nil || got.SearchableTracks != representationBackfillBatch+7 {
		t.Fatalf("resume/invalid source: %+v %v", got, err)
	}
	if err := s.store.db.QueryRow("SELECT COUNT(*) FROM audio_representation_vector WHERE valid=0").Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid source not marked unavailable: %d %v", count, err)
	}
	// A source mutation invalidates its projection immediately and is rechecked.
	if _, err := s.store.db.Exec("UPDATE audio_representation SET data='{' WHERE track='0'"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Search(context.Background(), searchQuery(100))
	if err != nil || after.SearchableTracks != got.SearchableTracks-1 || after.Fingerprint == got.Fingerprint {
		t.Fatalf("source corruption retained projection: %+v %v", after, err)
	}
	// Projection corruption itself is detected without parsing every source JSON.
	if _, err := s.store.db.Exec("UPDATE audio_representation_vector SET pooled=zeroblob(8) WHERE id=(SELECT id FROM audio_representation WHERE track='1')"); err != nil {
		t.Fatal(err)
	}
	after, err = s.Search(context.Background(), searchQuery(100))
	if err != nil || after.SearchableTracks != got.SearchableTracks-2 {
		t.Fatalf("projection corruption accepted: %+v %v", after, err)
	}
	// SQLite REPLACE does not reliably run DELETE triggers unless recursive
	// triggers are enabled. The INSERT trigger must invalidate that path too.
	if _, err := s.store.db.Exec(`INSERT OR REPLACE INTO audio_representation
 SELECT id,catalog,track,track_key,model,'{' FROM audio_representation WHERE track='2'`); err != nil {
		t.Fatal(err)
	}
	after, err = s.Search(context.Background(), searchQuery(100))
	if err != nil || after.SearchableTracks != got.SearchableTracks-3 {
		t.Fatalf("source replacement retained projection: %+v %v", after, err)
	}
	if err := s.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow("SELECT COUNT(*) FROM audio_representation_vector").Scan(&count); err != nil || count != 0 {
		t.Fatalf("clear left projections: %d %v", count, err)
	}
	after, err = s.Search(context.Background(), searchQuery(100))
	if err != nil || after.SearchableTracks != 0 || len(after.Matches) != 0 {
		t.Fatalf("clear left searchable results: %+v %v", after, err)
	}
}

func TestRepresentationSearchCancellationAndAtomicWrite(t *testing.T) {
	s := openRepresentationSearchStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, searchQuery(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel: %v", err)
	}
	// Hold the single connection: cancellation must also interrupt lock waiters.
	tx, err := s.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, searchErr := s.Search(ctx, searchQuery(1))
	_ = tx.Rollback()
	if !errors.Is(searchErr, context.DeadlineExceeded) {
		t.Fatalf("blocked search did not cancel: %v", searchErr)
	}
	// A failed projection write rolls back the authoritative insert as well.
	if _, err := s.store.db.Exec(`CREATE TRIGGER fail_projection BEFORE INSERT ON audio_representation_vector
 BEGIN SELECT RAISE(ABORT, 'test projection failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), storedRepresentation()); err == nil {
		t.Fatal("projection failure was ignored")
	}
	usage, err := s.Usage(context.Background())
	if err != nil || usage.Records != 0 {
		t.Fatalf("partial representation write committed: %+v %v", usage, err)
	}
}

func TestRepresentationSearchBatchUsesOneCompatibleView(t *testing.T) {
	s := openRepresentationSearchStore(t)
	putSearchRepresentation(t, s, searchRepresentation("a", "a", 0))
	putSearchRepresentation(t, s, searchRepresentation("b", "b", math.Pi/2))
	queries := []ports.AudioRepresentationQuery{searchQuery(1), searchQuery(1), searchQuery(0)}
	queries[1].Vector = []float32{0, 1}
	queries[2].Vector = nil
	results, err := s.SearchBatch(context.Background(), queries)
	if err != nil || len(results) != 3 || results[0].Matches[0].Representation.TrackID != "a" || results[1].Matches[0].Representation.TrackID != "b" {
		t.Fatalf("batch queries: %+v %v", results, err)
	}
	for _, result := range results {
		if result.SearchableTracks != 2 || result.Fingerprint != results[0].Fingerprint {
			t.Fatalf("batch split its read view: %+v", results)
		}
	}
	putSearchRepresentation(t, s, searchRepresentation("c", "c", math.Pi))
	newResults, err := s.SearchBatch(context.Background(), queries)
	if err != nil || newResults[0].Fingerprint == results[0].Fingerprint || results[0].SearchableTracks != 2 {
		t.Fatalf("new membership did not create a new owned view: %+v %v", newResults, err)
	}
	for _, result := range newResults {
		if result.SearchableTracks != 3 || result.Fingerprint != newResults[0].Fingerprint {
			t.Fatalf("new batch split its read view: %+v", newResults)
		}
	}
	// A concurrent writer may commit before or after the single batch view, but
	// it cannot slip between equal reference queries within that batch.
	equalQueries := make([]ports.AudioRepresentationQuery, maxRepresentationSearchQueries)
	for i := range equalQueries {
		equalQueries[i] = searchQuery(1)
	}
	written := make(chan error, 1)
	go func() { written <- s.Put(context.Background(), searchRepresentation("aa", "aa", 0)) }()
	concurrent, searchErr := s.SearchBatch(context.Background(), equalQueries)
	writeErr := <-written
	if searchErr != nil || writeErr != nil {
		t.Fatalf("concurrent batch: %v %v", searchErr, writeErr)
	}
	for _, result := range concurrent[1:] {
		if !reflect.DeepEqual(result, concurrent[0]) {
			t.Fatal("concurrent write split query results")
		}
	}
}

func TestRepresentationSearchBatchValidationAndProjectionFingerprint(t *testing.T) {
	s := openRepresentationSearchStore(t)
	a := storedRepresentation()
	putSearchRepresentation(t, s, a)
	for _, queries := range [][]ports.AudioRepresentationQuery{
		nil,
		make([]ports.AudioRepresentationQuery, maxRepresentationSearchQueries+1),
		{searchQuery(512), searchQuery(512), searchQuery(1)},
	} {
		if _, err := s.SearchBatch(context.Background(), queries); err == nil {
			t.Fatal("unbounded/empty batch accepted")
		}
	}
	for _, mismatch := range []string{"catalog", "model"} {
		queries := []ports.AudioRepresentationQuery{searchQuery(1), searchQuery(1)}
		if mismatch == "catalog" {
			queries[1].CatalogVersion += "other"
		} else {
			queries[1].Model.Pooling += "other"
		}
		if _, err := s.SearchBatch(context.Background(), queries); err == nil {
			t.Fatal("incompatible identities shared a batch")
		}
	}
	before, err := s.Search(context.Background(), searchQuery(0))
	if err != nil {
		t.Fatal(err)
	}
	// Include vector provenance even if a projection is replaced in place under
	// the same record ID. A view fingerprint must describe what was scanned.
	pooled := make([]byte, 8)
	binary.LittleEndian.PutUint32(pooled[4:], math.Float32bits(1))
	var stamp string
	if err := s.store.db.QueryRow("SELECT analyzed FROM audio_representation_vector WHERE id=?", a.ID).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	digest := representationProjectionDigest(a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), stamp, pooled)
	if _, err := s.store.db.Exec("UPDATE audio_representation_vector SET pooled=?,digest=? WHERE id=?", pooled, digest, a.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.Search(context.Background(), searchQuery(0))
	if err != nil || after.SearchableTracks != before.SearchableTracks || after.Fingerprint == before.Fingerprint {
		t.Fatalf("in-place projection change retained fingerprint: %+v %+v %v", before, after, err)
	}
	if _, err := s.Search(context.Background(), searchQuery(1)); err == nil {
		t.Fatal("projection vector inconsistent with authoritative source was returned")
	}
}
