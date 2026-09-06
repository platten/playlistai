package brute_test

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

const dim = 16

func randUnit(rng *rand.Rand) []float32 {
	v := make([]float32, dim)
	var s float64
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		s += float64(v[i]) * float64(v[i])
	}
	n := float32(math.Sqrt(s))
	for i := range v {
		v[i] /= n
	}
	return v
}

func buildCatalog(t *testing.T, n int, seed int64) *fakes.Catalog {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	rows := make([]fakes.CatalogTrack, n)
	for i := range rows {
		rows[i] = fakes.CatalogTrack{
			ID:      idFor(i),
			Display: "Artist - Track " + idFor(i),
			Audio:   randUnit(rng),
			Track:   randUnit(rng),
		}
	}
	return fakes.NewCatalog(dim, rows...)
}

func idFor(i int) string {
	return "t" + string(rune('A'+i/26)) + string(rune('a'+i%26))
}

// referenceSearch is an independent, deliberately-slow implementation over the
// same quantized data the engine sees. The engine must match it exactly.
func referenceSearch(cat ports.Catalog, q ports.SimilarityQuery) []ports.Match {
	k := q.K
	if k <= 0 {
		k = 20
	}
	exclude := map[string]struct{}{}
	for id := range q.Exclude {
		exclude[id] = struct{}{}
	}

	var all []ports.Match
	for row := 0; row < cat.Len(); row++ {
		id := cat.ID(row)
		if _, skip := exclude[id]; skip {
			continue
		}
		a, tk, _ := cat.RawRow(row)
		score := q.Weights[0]*cosF(q.AudioSum, a) + q.Weights[1]*cosF(q.TrackSum, tk)
		all = append(all, ports.Match{ID: id, Row: row, Score: score})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Row < all[j].Row
	})
	if k > len(all) {
		k = len(all)
	}
	return all[:k]
}

// cosF = dot(query, dequant(v)/||dequant(v)||); query is NOT renormalized.
func cosF(q []float32, v []int8) float32 {
	if len(q) != len(v) {
		return 0
	}
	var dot, nv float64
	for i := range v {
		f := float64(v[i]) / 127
		dot += float64(q[i]) * f
		nv += f * f
	}
	if nv == 0 {
		return 0
	}
	return float32(dot / math.Sqrt(nv))
}

func ids(ms []ports.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func mustSearch(t *testing.T, eng *brute.Engine, q ports.SimilarityQuery) []ports.Match {
	t.Helper()
	matches, err := eng.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestSearchMatchesReference(t *testing.T) {
	t.Parallel()
	cat := buildCatalog(t, 200, 1)
	eng := brute.New(cat)
	rng := rand.New(rand.NewSource(99))

	for trial := 0; trial < 20; trial++ {
		q := ports.SimilarityQuery{
			AudioSum: randUnit(rng),
			TrackSum: randUnit(rng),
			Weights:  [2]float32{rng.Float32(), rng.Float32()},
			K:        10,
		}
		got := ids(mustSearch(t, eng, q))
		want := ids(referenceSearch(cat, q))
		if len(got) != len(want) {
			t.Fatalf("trial %d: len %d != %d", trial, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("trial %d rank %d: got %s want %s\n got=%v\nwant=%v", trial, i, got[i], want[i], got, want)
			}
		}
	}
}

func TestParallelSearchMatchesSerial(t *testing.T) {
	t.Parallel()
	cat := buildCatalog(t, 5000, 23)
	serial := brute.NewWithWorkers(cat, 1)
	parallel := brute.NewWithWorkers(cat, 4)
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 12; trial++ {
		q := ports.SimilarityQuery{
			AudioSum: randUnit(rng),
			TrackSum: randUnit(rng),
			Weights:  [2]float32{rng.Float32(), rng.Float32()},
			K:        50,
			Exclude:  map[string]struct{}{idFor(trial * 17): {}},
		}
		got := mustSearch(t, parallel, q)
		want := mustSearch(t, serial, q)
		if len(got) != len(want) {
			t.Fatalf("trial %d: len %d != %d", trial, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("trial %d rank %d: parallel=%+v serial=%+v", trial, i, got[i], want[i])
			}
		}
	}
}

func TestParallelSearchHonorsCancellation(t *testing.T) {
	t.Parallel()
	eng := brute.NewWithWorkers(buildCatalog(t, 5000, 27), 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Search(ctx, ports.SimilarityQuery{K: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v, want context.Canceled", err)
	}
}

func TestSelfIsNearestThenExcluded(t *testing.T) {
	t.Parallel()
	cat := buildCatalog(t, 120, 7)
	eng := brute.New(cat)

	seed := "tCc" // row 2*26+2 = 54
	v, ok := cat.Vectors(seed)
	if !ok {
		t.Fatal("seed missing")
	}
	q := ports.SimilarityQuery{
		AudioSum: v.Audio,
		TrackSum: v.Track,
		Weights:  [2]float32{0.5, 0.5},
		K:        5,
	}

	self := mustSearch(t, eng, q)
	if self[0].ID != seed {
		t.Fatalf("seed should be its own nearest, got %s", self[0].ID)
	}

	q.Exclude = map[string]struct{}{seed: {}}
	excluded := mustSearch(t, eng, q)
	for _, m := range excluded {
		if m.ID == seed {
			t.Fatal("excluded id still present")
		}
	}
	if excluded[0].ID != self[1].ID {
		t.Fatalf("after excluding seed, #1 should be old #2 (%s), got %s", self[1].ID, excluded[0].ID)
	}
}

func TestWeightsSelectSpace(t *testing.T) {
	t.Parallel()
	// One track is a perfect audio match for the query but antipodal in track
	// space; another is the reverse.
	audioQ := randUnit(rand.New(rand.NewSource(3)))
	trackQ := randUnit(rand.New(rand.NewSource(4)))
	neg := func(v []float32) []float32 {
		o := make([]float32, len(v))
		for i := range v {
			o[i] = -v[i]
		}
		return o
	}
	filler := buildCatalog(t, 40, 11)
	rows := []fakes.CatalogTrack{
		{ID: "audioStar", Display: "A - audio", Audio: audioQ, Track: neg(trackQ)},
		{ID: "trackStar", Display: "A - track", Audio: neg(audioQ), Track: trackQ},
	}
	for i := 0; i < filler.Len(); i++ {
		id := filler.ID(i)
		v, _ := filler.Vectors(id)
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: "x - " + id, Audio: v.Audio, Track: v.Track})
	}
	cat := fakes.NewCatalog(dim, rows...)
	eng := brute.New(cat)

	byAudio := mustSearch(t, eng, ports.SimilarityQuery{AudioSum: audioQ, TrackSum: trackQ, Weights: [2]float32{1, 0}, K: 1})
	if byAudio[0].ID != "audioStar" {
		t.Fatalf("creativity=1 should pick the audio match, got %s", byAudio[0].ID)
	}
	byTrack := mustSearch(t, eng, ports.SimilarityQuery{AudioSum: audioQ, TrackSum: trackQ, Weights: [2]float32{0, 1}, K: 1})
	if byTrack[0].ID != "trackStar" {
		t.Fatalf("creativity=0 should pick the co-occurrence match, got %s", byTrack[0].ID)
	}
}

func TestKCapAndDeterminism(t *testing.T) {
	t.Parallel()
	cat := buildCatalog(t, 300, 5)
	eng := brute.New(cat)
	if eng.Len() != 300 {
		t.Fatalf("Len = %d", eng.Len())
	}
	q := ports.SimilarityQuery{
		AudioSum: randUnit(rand.New(rand.NewSource(1))),
		TrackSum: randUnit(rand.New(rand.NewSource(2))),
		Weights:  [2]float32{0.6, 0.4},
		K:        7,
	}
	a := mustSearch(t, eng, q)
	b := mustSearch(t, eng, q)
	if len(a) != 7 {
		t.Fatalf("K not honored: %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
	// descending
	for i := 1; i < len(a); i++ {
		if a[i-1].Score < a[i].Score {
			t.Fatalf("not descending: %v", a)
		}
	}
}

func TestEmptyAndZeroQuery(t *testing.T) {
	t.Parallel()
	if got := mustSearch(t, brute.New(fakes.NewCatalog(dim)), ports.SimilarityQuery{K: 5}); got != nil {
		t.Fatalf("empty catalog should return nil, got %v", got)
	}

	cat := buildCatalog(t, 10, 8)
	eng := brute.New(cat)
	// No usable query vectors → every score 0 → first K rows in row order.
	got := mustSearch(t, eng, ports.SimilarityQuery{K: 3})
	if len(got) != 3 || got[0].Row != 0 || got[1].Row != 1 || got[2].Row != 2 {
		t.Fatalf("zero query should yield rows 0,1,2; got %+v", got)
	}
}

func TestSearchHonorsCancellation(t *testing.T) {
	t.Parallel()
	eng := brute.New(buildCatalog(t, 5000, 12))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = &cancelAfterChecks{Context: ctx, cancel: cancel, remaining: 3}
	if _, err := eng.Search(ctx, ports.SimilarityQuery{K: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v, want context.Canceled", err)
	}
}

type cancelAfterChecks struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelAfterChecks) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
